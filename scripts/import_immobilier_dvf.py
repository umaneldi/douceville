#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["psycopg[binary]>=3.1", "tqdm>=4.66"]
# ///
"""Load the "demandes de valeurs foncières géolocalisées" (DVF, data.gouv.fr)
gzipped CSV into the `immobilier` table.

Usage:
    uv run scripts/import_immobilier_dvf.py \
        demandes-de-valeurs-foncieres-geolocalisees.csv.gz \
        [--dsn URL | --psql CMD] [--start-year YEAR] [--dry-run]

To import into the docker-compose database without a direct connection:
    uv run scripts/import_immobilier_dvf.py FILE.csv.gz \
        --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the data is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses. Run the server once beforehand so that the
migrations have created the table, and load the `quartier` table first
(scripts/import_contours_iris.py): `immobilier.iris` references it.

Only sales of a single built property are kept: rows with nature_mutation =
'Vente', surface_reelle_bati > 0, a valeur_fonciere and coordinates; a sale
(id_mutation) with several such rows is skipped entirely, since each row
repeats the total price of the sale. The file must be sorted by id_mutation
(as published).

Columns kept: date_mutation, nature_mutation, valeur_fonciere, id_parcelle,
surface_terrain, surface_reelle_bati. `iris` is the quartier whose contour
contains (longitude, latitude); rows outside every quartier are skipped.

The table is fully replaced (with --start-year YEAR: only rows dated from
YEAR on are replaced, and only those are read from the file). Everything runs
in a single transaction (nothing changes if the import fails).
"""

from __future__ import annotations

import argparse
import csv
import gzip
import os
import shlex
import subprocess
import sys
import time
from collections import Counter
from collections.abc import Iterator
from pathlib import Path

import psycopg
from tqdm import tqdm

BATCH_SIZE = 100_000


def parse_built(r: dict) -> tuple | None:
    """The table row (with lon, lat first) for a built sale row, else None."""
    if r["nature_mutation"] != "Vente":
        return None
    try:
        bati = int(r["surface_reelle_bati"] or 0)
        valeur = float(r["valeur_fonciere"]) if r["valeur_fonciere"] else None
        lon = float(r["longitude"]) if r["longitude"] else None
        lat = float(r["latitude"]) if r["latitude"] else None
        terrain = int(r["surface_terrain"]) if r["surface_terrain"] else None
    except ValueError:
        return None
    if bati <= 0 or valeur is None or lon is None or lat is None:
        return None
    return (
        lon,
        lat,
        r["date_mutation"],
        r["nature_mutation"],
        r["valeur_fonciere"],
        r["id_parcelle"],
        terrain,
        bati,
    )


def extract(
    path: Path, start_year: int | None, stats: Counter, bar: tqdm
) -> Iterator[list[tuple]]:
    """Yield the kept rows in batches; count read/skipped rows in stats."""
    batch: list[tuple] = []
    with open(path, "rb") as raw, gzip.open(raw, "rt", newline="", encoding="utf-8") as f:
        reader = csv.DictReader(f)
        cur_id, group, last_pos = None, [], 0

        def flush() -> None:
            if len(group) == 1:
                batch.append(group[0])
            elif group:
                stats["multi_building"] += len(group)

        for r in reader:
            stats["lines"] += 1
            if r["id_mutation"] != cur_id:
                flush()
                cur_id, group = r["id_mutation"], []
            if start_year is not None and int(r["date_mutation"][:4]) < start_year:
                continue
            row = parse_built(r)
            if row is not None:
                group.append(row)
            if len(batch) >= BATCH_SIZE:
                yield batch
                batch = []
                pos = raw.tell()
                bar.update(pos - last_pos)
                last_pos = pos
        flush()
        if batch:
            yield batch
        bar.update(raw.tell() - last_pos)


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
CREATE TEMP TABLE tmp_immobilier (
    lon DOUBLE PRECISION, lat DOUBLE PRECISION, date_mutation DATE,
    nature_mutation TEXT, valeur_fonciere NUMERIC(15, 2), id_parcelle TEXT,
    surface_terrain INTEGER, surface_reelle_bati INTEGER
) ON COMMIT DROP
"""


def clear_sql(start_year: int | None) -> str:
    if start_year is None:
        return "TRUNCATE immobilier"
    return f"DELETE FROM immobilier WHERE date_mutation >= make_date({int(start_year)}, 1, 1)"


# Run once per batch. Returns (read, inserted) for the batch; the other rows
# fall outside every quartier. LIMIT 1: a point on a shared border matches
# several IRIS.
INSERT = """
WITH r AS (
    INSERT INTO immobilier (iris, date_mutation, nature_mutation, valeur_fonciere,
                            id_parcelle, surface_terrain, surface_reelle_bati)
    SELECT q.iris, t.date_mutation, t.nature_mutation, t.valeur_fonciere,
           t.id_parcelle, t.surface_terrain, t.surface_reelle_bati
    FROM tmp_immobilier t
    CROSS JOIN LATERAL (
        SELECT iris FROM quartier
        WHERE ST_Intersects(contour, ST_SetSRID(ST_MakePoint(t.lon, t.lat), 4326))
        LIMIT 1
    ) q
    RETURNING 1
)
SELECT (SELECT count(*) FROM tmp_immobilier), (SELECT count(*) FROM r)
"""

CHECK = """
DO $$ BEGIN
    IF to_regclass('immobilier') IS NULL THEN
        RAISE EXCEPTION 'table immobilier not found: start the Go server once to run the migrations';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM quartier) THEN
        RAISE EXCEPTION 'table quartier is empty: run scripts/import_contours_iris.py first';
    END IF;
END $$
"""


def write_db(
    dsn: str, start_year: int | None, batches: Iterator[list[tuple]]
) -> tuple[int, int]:
    totals = [0, 0]
    with psycopg.connect(dsn) as conn, conn.cursor() as cur:
        try:
            cur.execute(CHECK)
        except psycopg.errors.RaiseException as e:
            sys.exit(e.diag.message_primary)
        cur.execute(CREATE_TMP)
        cur.execute(clear_sql(start_year))
        for batch in batches:
            with cur.copy("COPY tmp_immobilier FROM STDIN") as copy:
                for row in batch:
                    copy.write_row(row)
            cur.execute(INSERT)
            totals = [t + n for t, n in zip(totals, cur.fetchone())]
            cur.execute("TRUNCATE tmp_immobilier")
    return tuple(totals)


def write_psql(
    cmd: str, start_year: int | None, batches: Iterator[list[tuple]], bar: tqdm
) -> tuple[int, int]:
    """Same as write_db, streamed through a psql command (e.g. `docker exec -i <container> psql ...`).

    Each batch is COPYed then inserted; the per-batch result line printed by psql is
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

    totals = [0, 0]
    try:
        proc.stdin.write(
            "\\set ON_ERROR_STOP on\n\\set QUIET on\n\\pset tuples_only on\n\\pset format unaligned\n"
            f"BEGIN;\n{CHECK};\n{CREATE_TMP};\n{clear_sql(start_year)};\n"
        )
        for batch in batches:
            proc.stdin.write("COPY tmp_immobilier FROM STDIN;\n")
            proc.stdin.writelines(
                "\t".join(map(copy_field, row)) + "\n" for row in batch
            )
            proc.stdin.write(f"\\.\n{INSERT};\nTRUNCATE tmp_immobilier;\n")
            proc.stdin.flush()
            line = proc.stdout.readline()
            while line and not line.strip():
                line = proc.stdout.readline()
            if not line:  # psql exited on an error (reported on its stderr)
                fail()
            totals = [t + int(n) for t, n in zip(totals, line.strip().split("|"))]
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
        "csv", type=Path, help="DVF geolocated sales gzipped CSV from data.gouv.fr"
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
        help="only import (and replace) rows whose date_mutation is in YEAR or later "
        "(default: all years, table fully replaced)",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="parse and report, do not touch the database",
    )
    args = parser.parse_args()

    t0 = time.monotonic()
    stats: Counter = Counter()
    with tqdm(
        total=args.csv.stat().st_size,
        unit="B",
        unit_scale=True,
        desc="Reading" if args.dry_run else "Importing",
    ) as bar:
        batches = extract(args.csv, args.start_year, stats, bar)
        if args.dry_run:
            kept = 0
            for batch in batches:
                for row in batch[: max(0, 5 - kept)]:
                    tqdm.write(f"   {row}")
                kept += len(batch)
        elif args.psql:
            kept, inserted = write_psql(args.psql, args.start_year, batches, bar)
        else:
            kept, inserted = write_db(args.dsn or default_dsn(), args.start_year, batches)

    print(
        f"{stats['lines']} lines read; {kept} single-building sales kept, "
        f"{stats['multi_building']} rows of multi-building sales skipped"
    )
    if not args.dry_run:
        print(
            f"inserted {inserted}, skipped {kept - inserted} (outside every quartier) "
            f"in {time.monotonic() - t0:.1f}s"
        )


if __name__ == "__main__":
    main()
