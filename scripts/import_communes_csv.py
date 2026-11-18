#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["psycopg[binary]>=3.1"]
# ///
"""Upsert the INSEE "communes depuis 1943" CSV export into the `commune` table.

Usage:
    uv run scripts/import_communes_csv.py v_commune_depuis_1943.csv \
        [--dsn URL | --psql CMD] [--dry-run]

To import into the docker-compose database without a direct connection:
    uv run scripts/import_communes_csv.py v_commune_depuis_1943.csv \
        --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the data is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses. Run the server once beforehand so that the
migrations have created the table.

Only current communes are kept: rows with an empty DATE_FIN and TYPECOM "COM"
(municipal arrondissements "ARM" are skipped). Columns: COM -> code_commune,
NCCENR -> nom, DATE_DEBUT -> date_creation. Rows are keyed by code_commune:
existing rows are updated, new ones inserted. Rows are never deleted.
"""

from __future__ import annotations

import argparse
import csv
import os
import shlex
import subprocess
import sys
import time
from pathlib import Path

import psycopg


def extract(path: Path):
    stats = {"ended": 0, "not_com": 0}
    rows = {}
    with path.open(encoding="utf-8", newline="") as f:
        for r in csv.DictReader(f):
            if r["DATE_FIN"] != "":
                stats["ended"] += 1
                continue
            if r["TYPECOM"] != "COM":
                stats["not_com"] += 1
                continue
            rows[r["COM"]] = (r["COM"], r["NCCENR"], r["DATE_DEBUT"])
    return list(rows.values()), stats


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
    return str(v).replace("\\", "\\\\").replace("\t", "\\t").replace("\n", "\\n").replace("\r", "\\r")


def run_psql(cmd: str, sql: str) -> list[str]:
    """Pipe `sql` into the psql command `cmd`; return the fields of its last output line."""
    script = "\\set ON_ERROR_STOP on\n\\set QUIET on\n\\pset tuples_only on\n\\pset format unaligned\n" + sql
    try:
        proc = subprocess.run(shlex.split(cmd), input=script, text=True, stdout=subprocess.PIPE)
    except OSError as e:
        sys.exit(f"--psql: cannot run {cmd!r}: {e}")
    lines = [line for line in proc.stdout.splitlines() if line.strip()]
    if proc.returncode != 0 or not lines:
        sys.exit(f"--psql: import failed (exit code {proc.returncode}); nothing was committed")
    return lines[-1].split("|")


def default_dsn() -> str:
    load_dotenv(Path.cwd() / ".env")
    env = os.environ.get
    return (
        f"postgresql://{env('POSTGRES_USER', 'douceville')}:{env('POSTGRES_PASSWORD', '')}"
        f"@{env('POSTGRES_HOST', 'localhost')}:{env('POSTGRES_PORT', '5432')}"
        f"/{env('POSTGRES_DB', 'douceville')}"
    )


CREATE_TMP = """
CREATE TEMP TABLE tmp_commune (
    code_commune TEXT, nom TEXT, date_creation DATE
) ON COMMIT DROP
"""

UPSERT = """
INSERT INTO commune (code_commune, nom, date_creation)
SELECT code_commune, nom, date_creation
FROM tmp_commune
ON CONFLICT (code_commune) DO UPDATE SET
    nom           = EXCLUDED.nom,
    date_creation = EXCLUDED.date_creation
RETURNING (xmax = 0) AS inserted
"""


def write_db(dsn: str, rows: list[tuple]) -> tuple[int, int]:
    with psycopg.connect(dsn) as conn, conn.cursor() as cur:
        cur.execute("SELECT to_regclass('commune')")
        if cur.fetchone()[0] is None:
            sys.exit("table 'commune' not found: start the Go server once to run the migrations")
        cur.execute(CREATE_TMP)
        with cur.copy("COPY tmp_commune FROM STDIN") as copy:
            for row in rows:
                copy.write_row(row)
        cur.execute(UPSERT)
        flags = [r[0] for r in cur.fetchall()]
        inserted = sum(flags)
        return inserted, len(flags) - inserted


def write_psql(cmd: str, rows: list[tuple]) -> tuple[int, int]:
    """Same as write_db, through a psql command (e.g. `docker exec -i <container> psql ...`)."""
    data = "".join("\t".join(map(copy_field, row)) + "\n" for row in rows)
    inserted, updated = run_psql(cmd, f"""
BEGIN;
DO $$ BEGIN
    IF to_regclass('commune') IS NULL THEN
        RAISE EXCEPTION 'table commune not found: start the Go server once to run the migrations';
    END IF;
END $$;
{CREATE_TMP};
COPY tmp_commune FROM STDIN;
{data}\\.
WITH r AS ({UPSERT}) SELECT count(*) FILTER (WHERE inserted), count(*) FILTER (WHERE NOT inserted) FROM r;
COMMIT;
""")
    return int(inserted), int(updated)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("csv", type=Path, help="v_commune_depuis_1943.csv from INSEE")
    parser.add_argument("--dsn", help="PostgreSQL URL (default: built from POSTGRES_* / .env)")
    parser.add_argument("--psql", metavar="CMD",
                        help="pipe the import into this psql command instead of connecting with --dsn, e.g. "
                             "'docker exec -i douceville-geodb-1 psql -U douceville -d douceville'")
    parser.add_argument("--dry-run", action="store_true", help="parse and report, do not touch the database")
    args = parser.parse_args()

    t0 = time.monotonic()
    rows, stats = extract(args.csv)
    print(f"{len(rows)} communes to upsert; skipped: {stats['ended']} ended (DATE_FIN set), "
          f"{stats['not_com']} not of type COM")

    if args.dry_run:
        for row in rows[:5]:
            print("  ", row)
        return

    if args.psql:
        inserted, updated = write_psql(args.psql, rows)
    else:
        inserted, updated = write_db(args.dsn or default_dsn(), rows)
    print(f"Inserted {inserted}, updated {updated} rows in {time.monotonic() - t0:.1f}s")


if __name__ == "__main__":
    main()
