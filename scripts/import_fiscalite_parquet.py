#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["pyarrow>=15", "psycopg[binary]>=3.1", "tqdm>=4.66"]
# ///
"""Upsert the DGFiP "fiscalité locale des particuliers" Parquet export
(data.gouv.fr) into the `fiscalite` table.

Usage:
    uv run scripts/import_fiscalite_parquet.py \
        fiscalite-locale-des-particuliers-geo.parquet \
        [--dsn URL | --psql CMD] [--start-year YEAR] [--dry-run]

To import into the docker-compose database without a direct connection:
    uv run scripts/import_fiscalite_parquet.py FILE.parquet \
        --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the data is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses. Run the server once beforehand so that the
migrations have created the table, and load the `commune` table first
(scripts/import_communes_csv.py): `fiscalite.code_commune` references it.

Columns: INSEE COM -> code_commune, EXERCICE -> year, Taux_Global_TFB,
Taux_Plein_TEOM, Taux_Global_TFNB, Taux_Global_TH, OPTEPCI (lowercased).
Rows are keyed by (code_commune, year): existing rows are updated, new ones
inserted. Rows are never deleted. Rows whose code is not in `commune` are
skipped. With --start-year YEAR, only rows whose EXERCICE is >= YEAR are
imported. Everything runs in a single transaction (nothing is committed if the
import fails).
"""

from __future__ import annotations

import argparse
import math
import os
import shlex
import subprocess
import sys
import time
from collections.abc import Iterator
from pathlib import Path

import psycopg
from tqdm import tqdm
import pyarrow.compute as pc
import pyarrow.parquet as pq

COLUMNS = [
    "INSEE COM",
    "EXERCICE",
    "Taux_Global_TFB",
    "Taux_Plein_TEOM",
    "Taux_Global_TFNB",
    "Taux_Global_TH",
    "OPTEPCI",
]
YEAR = "EXERCICE"


def finite(v) -> float | None:
    return v if v is not None and math.isfinite(v) else None


BATCH_SIZE = 100_000


def extract(pf: pq.ParquetFile, start_year: int | None) -> Iterator[list[tuple]]:
    """Yield the rows of the Parquet file in batches, mapped to the table's columns.

    If start_year is set, only rows whose EXERCICE is >= start_year are yielded.
    """
    for batch in pf.iter_batches(batch_size=BATCH_SIZE, columns=COLUMNS):
        if start_year is not None:
            batch = batch.filter(pc.greater_equal(batch.column(YEAR), start_year))
        if batch.num_rows:
            yield [row_of(r) for r in batch.to_pylist()]


def count_rows(pf: pq.ParquetFile, start_year: int | None) -> int:
    """Number of rows extract() will yield."""
    if start_year is None:
        return pf.metadata.num_rows
    return sum(
        pc.sum(pc.greater_equal(b.column(YEAR), start_year)).as_py() or 0
        for b in pf.iter_batches(batch_size=BATCH_SIZE, columns=[YEAR])
    )


def row_of(r: dict) -> tuple:
    return (
        r["INSEE COM"],
        r["EXERCICE"],
        finite(r["Taux_Global_TFB"]),
        finite(r["Taux_Plein_TEOM"]),
        finite(r["Taux_Global_TFNB"]),
        finite(r["Taux_Global_TH"]),
        r["OPTEPCI"],
    )


def load_dotenv(path: Path) -> None:
    if not path.is_file():
        return
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        os.environ.setdefault(key.strip(), value.strip().strip("'\""))


def copy_field(v) -> str:
    """A value in COPY text format."""
    if v is None:
        return r"\N"
    if isinstance(v, bool):
        return "t" if v else "f"
    return (
        str(v)
        .replace("\\", "\\\\")
        .replace("\t", "\\t")
        .replace("\n", "\\n")
        .replace("\r", "\\r")
    )


def default_dsn() -> str:
    load_dotenv(Path.cwd() / ".env")
    env = os.environ.get
    return (
        f"postgresql://{env('POSTGRES_USER', 'douceville')}:{env('POSTGRES_PASSWORD', '')}"
        f"@{env('POSTGRES_HOST', 'localhost')}:{env('POSTGRES_PORT', '5432')}"
        f"/{env('POSTGRES_DB', 'douceville')}"
    )


CREATE_TMP = """
CREATE TEMP TABLE tmp_fiscalite (
    code_commune TEXT, year INTEGER, taux_global_tfb DOUBLE PRECISION,
    taux_plein_teom DOUBLE PRECISION, taux_global_tfnb DOUBLE PRECISION,
    taux_global_th DOUBLE PRECISION, optepci TEXT
) ON COMMIT DROP
"""

# Run once per batch. Returns (read, inserted, updated) for the batch;
# read - inserted - updated rows were skipped.
UPSERT = """
WITH r AS (
    INSERT INTO fiscalite (code_commune, year, taux_global_tfb, taux_plein_teom,
                           taux_global_tfnb, taux_global_th, optepci)
    SELECT t.code_commune, t.year, t.taux_global_tfb, t.taux_plein_teom,
           t.taux_global_tfnb, t.taux_global_th, t.optepci
    FROM tmp_fiscalite t
    JOIN commune c ON c.code_commune = t.code_commune
    ON CONFLICT (code_commune, year) DO UPDATE SET
        taux_global_tfb  = EXCLUDED.taux_global_tfb,
        taux_plein_teom  = EXCLUDED.taux_plein_teom,
        taux_global_tfnb = EXCLUDED.taux_global_tfnb,
        taux_global_th   = EXCLUDED.taux_global_th,
        optepci          = EXCLUDED.optepci
    RETURNING (xmax = 0) AS inserted
)
SELECT (SELECT count(*) FROM tmp_fiscalite),
       count(*) FILTER (WHERE inserted), count(*) FILTER (WHERE NOT inserted)
FROM r
"""

CHECK = """
DO $$ BEGIN
    IF to_regclass('fiscalite') IS NULL THEN
        RAISE EXCEPTION 'table fiscalite not found: start the Go server once to run the migrations';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM commune) THEN
        RAISE EXCEPTION 'table commune is empty: run scripts/import_communes_csv.py first';
    END IF;
END $$
"""


def write_db(
    dsn: str, batches: Iterator[list[tuple]], bar: tqdm
) -> tuple[int, int, int]:
    totals = [0, 0, 0]
    with psycopg.connect(dsn) as conn, conn.cursor() as cur:
        try:
            cur.execute(CHECK)
        except psycopg.errors.RaiseException as e:
            sys.exit(e.diag.message_primary)
        cur.execute(CREATE_TMP)
        for batch in batches:
            with cur.copy("COPY tmp_fiscalite FROM STDIN") as copy:
                for row in batch:
                    copy.write_row(row)
            cur.execute(UPSERT)
            totals = [t + n for t, n in zip(totals, cur.fetchone())]
            cur.execute("TRUNCATE tmp_fiscalite")
            bar.update(len(batch))
    return tuple(totals)


def write_psql(
    cmd: str, batches: Iterator[list[tuple]], bar: tqdm
) -> tuple[int, int, int]:
    """Same as write_db, streamed through a psql command (e.g. `docker exec -i <container> psql ...`).

    Each batch is COPYed then upserted; the per-batch result line printed by psql is
    awaited before sending the next batch, so the progress bar follows the database.
    """
    try:
        proc = subprocess.Popen(
            shlex.split(cmd), stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True
        )
    except OSError as e:
        sys.exit(f"--psql: cannot run {cmd!r}: {e}")

    def fail() -> None:
        try:
            proc.stdin.close()
        except BrokenPipeError:
            pass
        proc.wait()
        bar.close()
        sys.exit(
            f"--psql: import failed (exit code {proc.returncode}); nothing was committed"
        )

    totals = [0, 0, 0]
    try:
        proc.stdin.write(
            "\\set ON_ERROR_STOP on\n\\set QUIET on\n\\pset tuples_only on\n\\pset format unaligned\n"
            f"BEGIN;\n{CHECK};\n{CREATE_TMP};\n"
        )
        for batch in batches:
            proc.stdin.write("COPY tmp_fiscalite FROM STDIN;\n")
            proc.stdin.writelines(
                "\t".join(map(copy_field, row)) + "\n" for row in batch
            )
            proc.stdin.write(f"\\.\n{UPSERT};\nTRUNCATE tmp_fiscalite;\n")
            proc.stdin.flush()
            line = proc.stdout.readline()
            while line and not line.strip():
                line = proc.stdout.readline()
            if not line:  # psql exited on an error (reported on its stderr)
                fail()
            totals = [t + int(n) for t, n in zip(totals, line.strip().split("|"))]
            bar.update(len(batch))
        proc.stdin.write("COMMIT;\n")
        proc.stdin.close()
    except BrokenPipeError:
        fail()
    proc.stdout.read()
    if proc.wait() != 0:
        fail()
    return tuple(totals)


def main() -> None:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "parquet", type=Path, help="fiscalité locale Parquet file from data.gouv.fr"
    )
    parser.add_argument(
        "--dsn", help="PostgreSQL URL (default: built from POSTGRES_* / .env)"
    )
    parser.add_argument(
        "--psql",
        metavar="CMD",
        help="pipe the import into this psql command instead of connecting with --dsn, e.g. "
        "'docker exec -i douceville-geodb-1 psql -U douceville -d douceville'",
    )
    parser.add_argument(
        "--start-year",
        type=int,
        metavar="YEAR",
        help="only import rows whose EXERCICE is >= YEAR (default: all years)",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="parse and report, do not touch the database",
    )
    args = parser.parse_args()

    t0 = time.monotonic()
    pf = pq.ParquetFile(args.parquet)
    batches = extract(pf, args.start_year)

    with tqdm(
        total=count_rows(pf, args.start_year),
        unit="row",
        unit_scale=True,
        desc="Reading" if args.dry_run else "Importing",
    ) as bar:
        if args.dry_run:
            n = 0
            for batch in batches:
                for row in batch[: max(0, 5 - n)]:
                    tqdm.write(f"   {row}")
                n += len(batch)
                bar.update(len(batch))
        elif args.psql:
            read, inserted, updated = write_psql(args.psql, batches, bar)
        else:
            read, inserted, updated = write_db(args.dsn or default_dsn(), batches, bar)

    if args.dry_run:
        print(f"{n} rows read")
        return
    print(
        f"{read} rows read; inserted {inserted}, updated {updated}, "
        f"skipped {read - inserted - updated} (code_commune not in commune) in {time.monotonic() - t0:.1f}s"
    )


if __name__ == "__main__":
    main()
