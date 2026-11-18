#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["pyarrow>=15", "psycopg[binary]>=3.1"]
# ///
"""Upsert the SSMSI "base statistique communale de la délinquance enregistrée"
Parquet export (data.gouv.fr) into the `securite` table.

Usage:
    uv run scripts/import_securite_parquet.py \
        donnee-comm-data.gouv-parquet-2025-geographie2026-produit-le2026-06-25.parquet \
        [--dsn URL | --psql CMD] [--dry-run]

To import into the docker-compose database without a direct connection:
    uv run scripts/import_securite_parquet.py FILE.parquet \
        --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the data is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses. Run the server once beforehand so that the
migrations have created the table, and load the `commune` table first
(scripts/import_communes_csv.py): `securite.code_commune` references it.

Columns: CODGEO_2026 -> code_commune, annee, indicateur, unite_de_compte,
nombre, taux_pour_mille, est_diffuse ('diff' -> true, 'ndiff' -> false).
Rows are keyed by (code_commune, annee, indicateur): existing rows are updated,
new ones inserted. Rows are never deleted. Rows whose code is not in `commune`
(municipal arrondissements of Paris, Lyon and Marseille, whose parent commune
is already in the file) are skipped.
The file has millions of rows: it is read and sent in batches.
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
import pyarrow.parquet as pq

COLUMNS = ["CODGEO_2026", "annee", "indicateur", "unite_de_compte", "nombre", "taux_pour_mille", "est_diffuse"]
DIFFUSE = {"diff": True, "ndiff": False}


def finite(v) -> float | None:
    return v if v is not None and math.isfinite(v) else None


def extract(path: Path) -> Iterator[tuple]:
    """Yield the rows of the Parquet file, mapped to the table's columns."""
    for batch in pq.ParquetFile(path).iter_batches(batch_size=100_000, columns=COLUMNS):
        for r in batch.to_pylist():
            diffuse = DIFFUSE.get(r["est_diffuse"])
            if diffuse is None:
                sys.exit(f"unexpected est_diffuse value {r['est_diffuse']!r}")
            yield (
                r["CODGEO_2026"], r["annee"], r["indicateur"], r["unite_de_compte"],
                r["nombre"], finite(r["taux_pour_mille"]), diffuse,
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
    return str(v).replace("\\", "\\\\").replace("\t", "\\t").replace("\n", "\\n").replace("\r", "\\r")


def default_dsn() -> str:
    load_dotenv(Path.cwd() / ".env")
    env = os.environ.get
    return (
        f"postgresql://{env('POSTGRES_USER', 'douceville')}:{env('POSTGRES_PASSWORD', '')}"
        f"@{env('POSTGRES_HOST', 'localhost')}:{env('POSTGRES_PORT', '5432')}"
        f"/{env('POSTGRES_DB', 'douceville')}"
    )


CREATE_TMP = """
CREATE TEMP TABLE tmp_securite (
    code_commune TEXT, annee INTEGER, indicateur TEXT, unite_de_compte TEXT,
    nombre INTEGER, taux_pour_mille DOUBLE PRECISION, est_diffuse BOOLEAN
) ON COMMIT DROP
"""

# Returns (read, inserted, updated); read - inserted - updated rows were skipped.
UPSERT = """
WITH r AS (
    INSERT INTO securite (code_commune, annee, indicateur, unite_de_compte,
                          nombre, taux_pour_mille, est_diffuse)
    SELECT t.code_commune, t.annee, t.indicateur, t.unite_de_compte,
           t.nombre, t.taux_pour_mille, t.est_diffuse
    FROM tmp_securite t
    JOIN commune c ON c.code_commune = t.code_commune
    ON CONFLICT (code_commune, annee, indicateur) DO UPDATE SET
        unite_de_compte = EXCLUDED.unite_de_compte,
        nombre          = EXCLUDED.nombre,
        taux_pour_mille = EXCLUDED.taux_pour_mille,
        est_diffuse     = EXCLUDED.est_diffuse
    RETURNING (xmax = 0) AS inserted
)
SELECT (SELECT count(*) FROM tmp_securite),
       count(*) FILTER (WHERE inserted), count(*) FILTER (WHERE NOT inserted)
FROM r
"""

CHECK = """
DO $$ BEGIN
    IF to_regclass('securite') IS NULL THEN
        RAISE EXCEPTION 'table securite not found: start the Go server once to run the migrations';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM commune) THEN
        RAISE EXCEPTION 'table commune is empty: run scripts/import_communes_csv.py first';
    END IF;
END $$
"""


def write_db(dsn: str, rows: Iterator[tuple]) -> tuple[int, int, int]:
    with psycopg.connect(dsn) as conn, conn.cursor() as cur:
        try:
            cur.execute(CHECK)
        except psycopg.errors.RaiseException as e:
            sys.exit(e.diag.message_primary)
        cur.execute(CREATE_TMP)
        with cur.copy("COPY tmp_securite FROM STDIN") as copy:
            for row in rows:
                copy.write_row(row)
        cur.execute(UPSERT)
        return cur.fetchone()


def write_psql(cmd: str, rows: Iterator[tuple]) -> tuple[int, int, int]:
    """Same as write_db, streamed through a psql command (e.g. `docker exec -i <container> psql ...`)."""
    try:
        proc = subprocess.Popen(shlex.split(cmd), stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
    except OSError as e:
        sys.exit(f"--psql: cannot run {cmd!r}: {e}")
    try:
        proc.stdin.write("\\set ON_ERROR_STOP on\n\\set QUIET on\n\\pset tuples_only on\n\\pset format unaligned\n"
                         f"BEGIN;\n{CHECK};\n{CREATE_TMP};\nCOPY tmp_securite FROM STDIN;\n")
        for row in rows:
            proc.stdin.write("\t".join(map(copy_field, row)) + "\n")
        proc.stdin.write(f"\\.\n{UPSERT};\nCOMMIT;\n")
        proc.stdin.close()
    except BrokenPipeError:
        # psql stopped on an error (reported below); close without flushing again.
        try:
            proc.stdin.close()
        except BrokenPipeError:
            pass
    out = proc.stdout.read()
    proc.wait()
    lines = [line for line in out.splitlines() if line.strip()]
    if proc.returncode != 0 or not lines:
        sys.exit(f"--psql: import failed (exit code {proc.returncode}); nothing was committed")
    read, inserted, updated = lines[-1].split("|")
    return int(read), int(inserted), int(updated)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("parquet", type=Path, help="communal Parquet file from data.gouv.fr")
    parser.add_argument("--dsn", help="PostgreSQL URL (default: built from POSTGRES_* / .env)")
    parser.add_argument("--psql", metavar="CMD",
                        help="pipe the import into this psql command instead of connecting with --dsn, e.g. "
                             "'docker exec -i douceville-geodb-1 psql -U douceville -d douceville'")
    parser.add_argument("--dry-run", action="store_true", help="parse and report, do not touch the database")
    args = parser.parse_args()

    t0 = time.monotonic()
    rows = extract(args.parquet)

    if args.dry_run:
        n = 0
        for n, row in enumerate(rows, 1):
            if n <= 5:
                print("  ", row)
        print(f"{n} rows read")
        return

    if args.psql:
        read, inserted, updated = write_psql(args.psql, rows)
    else:
        read, inserted, updated = write_db(args.dsn or default_dsn(), rows)
    print(f"{read} rows read; inserted {inserted}, updated {updated}, "
          f"skipped {read - inserted - updated} (code_commune not in commune) in {time.monotonic() - t0:.1f}s")


if __name__ == "__main__":
    main()
