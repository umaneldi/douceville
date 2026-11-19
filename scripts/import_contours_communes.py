#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["psycopg[binary]>=3.1", "tqdm>=4.66"]
# ///
"""Load commune boundaries (Etalab "contours administratifs" GeoJSON) into
`commune.contour`.

Usage:
    uv run scripts/import_contours_communes.py communes-50m.geojson.gz \
        [--dsn URL | --psql CMD] [--dry-run]

Source (use the same COG year as the `commune` table, e.g. 2026):
    https://etalab-datasets.geo.data.gouv.fr/contours-administratifs/2026/geojson/communes-50m.geojson.gz
The 50 m simplified variant is precise enough for a choropleth and keeps the
API responses small. Plain `.geojson` and gzipped `.geojson.gz` are accepted.

To import into the docker-compose database without a direct connection:
    uv run scripts/import_contours_communes.py communes-50m.geojson.gz \
        --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the data is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses. Run the server once beforehand so that the
migrations have created the `contour` column, and load the `commune` table
first (scripts/import_communes_csv.py).

Features are matched on their `code` property (INSEE code) against
commune.code_commune; geometries are stored as MultiPolygon in WGS84 (SRID
4326). Features whose code is not in `commune` are skipped and counted; rows
are never inserted nor deleted. Everything runs in a single transaction.
"""

from __future__ import annotations

import argparse
import gzip
import json
import os
import shlex
import subprocess
import sys
import time
from pathlib import Path

import psycopg
from tqdm import tqdm


def extract(path: Path) -> tuple[list[tuple[str, str]], int]:
    """Return [(code, geometry GeoJSON)] and the number of features without geometry."""
    opener = gzip.open if path.suffix == ".gz" else open
    with opener(path, "rt", encoding="utf-8") as f:
        data = json.load(f)
    rows: dict[str, tuple[str, str]] = {}
    no_geom = 0
    for feat in tqdm(data["features"], desc="Reading", unit="feature", unit_scale=True):
        geom = feat.get("geometry")
        if not geom:
            no_geom += 1
            continue
        code = feat["properties"]["code"]
        rows[code] = (code, json.dumps(geom, separators=(",", ":")))
    return list(rows.values()), no_geom


def load_dotenv(path: Path) -> None:
    if not path.is_file():
        return
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        os.environ.setdefault(key.strip(), value.strip().strip("'\""))


def copy_field(v: object) -> str:
    """A value in COPY text format."""
    if v is None:
        return r"\N"
    return (
        str(v)
        .replace("\\", "\\\\")
        .replace("\t", "\\t")
        .replace("\n", "\\n")
        .replace("\r", "\\r")
    )


def run_psql(cmd: str, sql: str) -> list[str]:
    """Pipe `sql` into the psql command `cmd`; return the fields of its last output line."""
    script = (
        "\\set ON_ERROR_STOP on\n\\set QUIET on\n\\pset tuples_only on\n\\pset format unaligned\n"
        + sql
    )
    try:
        proc = subprocess.run(
            shlex.split(cmd), input=script, text=True, stdout=subprocess.PIPE, check=False
        )
    except OSError as e:
        sys.exit(f"--psql: cannot run {cmd!r}: {e}")
    lines = [line for line in proc.stdout.splitlines() if line.strip()]
    if proc.returncode != 0 or not lines:
        sys.exit(
            f"--psql: import failed (exit code {proc.returncode}); nothing was committed"
        )
    return lines[-1].split("|")


def default_dsn() -> str:
    load_dotenv(Path.cwd() / ".env")
    env = os.environ.get
    return (
        f"postgresql://{env('POSTGRES_USER', 'douceville')}:{env('POSTGRES_PASSWORD', '')}"
        f"@{env('POSTGRES_HOST', 'localhost')}:{env('POSTGRES_PORT', '5432')}"
        f"/{env('POSTGRES_DB', 'douceville')}"
    )


CHECK = """
DO $$ BEGIN
    IF to_regclass('commune') IS NULL OR NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'commune' AND column_name = 'contour'
    ) THEN
        RAISE EXCEPTION 'column commune.contour not found: start the Go server once to run the migrations';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM commune) THEN
        RAISE EXCEPTION 'table commune is empty: run scripts/import_communes_csv.py first';
    END IF;
END $$
"""

CREATE_TMP = """
CREATE TEMP TABLE tmp_contour (code TEXT, geom TEXT) ON COMMIT DROP
"""

# Returns (updated, skipped, communes still without contour).
UPDATE = """
WITH u AS (
    UPDATE commune c
    SET contour = ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON(t.geom), 4326))
    FROM tmp_contour t
    WHERE t.code = c.code_commune
    RETURNING c.code_commune
)
SELECT (SELECT count(*) FROM u),
       (SELECT count(*) FROM tmp_contour) - (SELECT count(*) FROM u),
       (SELECT count(*) FROM commune WHERE contour IS NULL
            AND code_commune NOT IN (SELECT code_commune FROM u))
"""


def write_db(dsn: str, rows: list[tuple[str, str]]) -> tuple[int, int, int]:
    with psycopg.connect(dsn) as conn, conn.cursor() as cur:
        try:
            cur.execute(CHECK)
        except psycopg.errors.RaiseException as e:
            sys.exit(e.diag.message_primary)
        cur.execute(CREATE_TMP)
        with cur.copy("COPY tmp_contour FROM STDIN") as copy:
            for row in tqdm(rows, desc="Sending", unit="row", unit_scale=True):
                copy.write_row(row)
        cur.execute(UPDATE)
        return tuple(cur.fetchone())


def write_psql(cmd: str, rows: list[tuple[str, str]]) -> tuple[int, int, int]:
    """Same as write_db, through a psql command (e.g. `docker exec -i <container> psql ...`)."""
    data = "".join(
        "\t".join(map(copy_field, row)) + "\n"
        for row in tqdm(rows, desc="Serializing", unit="row", unit_scale=True)
    )
    fields = run_psql(
        cmd,
        f"""
BEGIN;
{CHECK};
{CREATE_TMP};
COPY tmp_contour FROM STDIN;
{data}\\.
{UPDATE};
COMMIT;
""",
    )
    updated, skipped, missing = (int(n) for n in fields)
    return updated, skipped, missing


def main() -> None:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "geojson", type=Path, help="communes-*.geojson[.gz] from Etalab"
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
        "--dry-run",
        action="store_true",
        help="parse and report, do not touch the database",
    )
    args = parser.parse_args()

    t0 = time.monotonic()
    rows, no_geom = extract(args.geojson)
    print(f"{len(rows)} contours read; skipped {no_geom} features without geometry")

    if args.dry_run:
        for code, geom in rows[:5]:
            print("  ", code, geom[:80] + "…")
        return

    if args.psql:
        updated, skipped, missing = write_psql(args.psql, rows)
    else:
        updated, skipped, missing = write_db(args.dsn or default_dsn(), rows)
    print(
        f"Updated {updated} communes, skipped {skipped} contours (code not in commune); "
        f"{missing} communes still without contour ({time.monotonic() - t0:.1f}s)"
    )


if __name__ == "__main__":
    main()
