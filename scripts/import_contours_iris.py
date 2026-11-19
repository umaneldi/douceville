#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["psycopg[binary]>=3.1", "tqdm>=4.66"]
# ///
"""Load IRIS neighbourhoods (IGN "contours IRIS") into the `quartier` table.

Usage:
    uv run scripts/import_contours_iris.py [contours_iris.json ...] \
        [--url WFS_URL] [--save out.json[.gz]] [--dsn URL | --psql CMD] [--dry-run]

Without a file argument, every feature is downloaded from the IGN Géoplateforme
WFS (STATISTICALUNITS.IRIS:contours_iris), page by page (the server returns at
most 5000 features per request). `--save` writes the merged FeatureCollection
to disk so later runs can skip the download. Plain `.json`/`.geojson` and
gzipped `.gz` files are accepted; a warning is printed when a file is a
truncated WFS export (numberReturned < numberMatched).

To import into the docker-compose database without a direct connection:
    uv run scripts/import_contours_iris.py \
        --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the data is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses. Run the server once beforehand so that the
migrations have created the `quartier` table, and load the `commune` table
first (scripts/import_communes_csv.py).

Mapping: code_iris -> quartier.iris (9-character code, primary key),
code_insee -> quartier.code_commune (municipal arrondissements of Paris,
Marseille and Lyon are mapped to their parent commune, which is the one in
`commune`), nom_iris, type_iris, geometry -> quartier.contour (MultiPolygon,
SRID 4326). Rows are upserted on `iris`; features whose commune is not in
`commune` are skipped and counted. Everything runs in a single transaction.
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
import urllib.request
from pathlib import Path

import psycopg
from tqdm import tqdm

WFS_URL = (
    "https://data.geopf.fr/wfs/ows?SERVICE=WFS&VERSION=2.0.0&REQUEST=GetFeature"
    "&TYPENAMES=STATISTICALUNITS.IRIS:contours_iris"
    "&OUTPUTFORMAT=application/json&SRSNAME=EPSG:4326&SORTBY=cleabs"
)
PAGE_SIZE = 5000

Row = tuple[str, str, str, str, str]  # iris, code_commune, nom_iris, type_iris, geom


def fetch_page(url: str, start: int, retries: int = 4) -> dict:
    sep = "&" if "?" in url else "?"
    page_url = f"{url}{sep}COUNT={PAGE_SIZE}&STARTINDEX={start}"
    for attempt in range(retries):
        try:
            with urllib.request.urlopen(page_url, timeout=300) as resp:
                return json.load(resp)
        except (OSError, json.JSONDecodeError) as e:
            if attempt == retries - 1:
                sys.exit(f"WFS download failed at STARTINDEX={start}: {e}")
            time.sleep(2**attempt)
    raise AssertionError("unreachable")


def download(url: str) -> list[dict]:
    """All features of the WFS layer, paginated with COUNT/STARTINDEX."""
    first = fetch_page(url, 0)
    total = first.get("numberMatched")
    features = list(first["features"])
    with tqdm(total=total, initial=len(features), desc="Downloading", unit="feature") as bar:
        while len(first["features"]) == PAGE_SIZE and (total is None or len(features) < total):
            first = fetch_page(url, len(features))
            features.extend(first["features"])
            bar.update(len(first["features"]))
    if total is not None and len(features) != total:
        print(f"warning: downloaded {len(features)} features, server announced {total}")
    return features


def read_file(path: Path) -> list[dict]:
    opener = gzip.open if path.suffix == ".gz" else open
    with opener(path, "rt", encoding="utf-8") as f:
        data = json.load(f)
    matched, returned = data.get("numberMatched"), data.get("numberReturned")
    if isinstance(matched, int) and isinstance(returned, int) and returned < matched:
        print(
            f"warning: {path} is a truncated WFS export ({returned} of {matched} features); "
            "run without a file argument to download everything"
        )
    return data["features"]


def code_commune(code: str | None) -> str | None:
    """INSEE code; municipal arrondissements are mapped to their parent commune."""
    code = (code or "").strip().upper() or None
    if code is None:
        return None
    if "75101" <= code <= "75120":
        return "75056"  # Paris
    if "13201" <= code <= "13216":
        return "13055"  # Marseille
    if "69381" <= code <= "69389":
        return "69123"  # Lyon
    return code


def extract(features: list[dict]) -> tuple[list[Row], int]:
    """Return rows deduplicated on iris, and the number of unusable features."""
    rows: dict[str, Row] = {}
    bad = 0
    for feat in tqdm(features, desc="Reading", unit="feature", unit_scale=True):
        geom = feat.get("geometry")
        p = feat.get("properties") or {}
        iris = (p.get("code_iris") or "").strip()
        commune = code_commune(p.get("code_insee"))
        if not geom or not iris or not commune:
            bad += 1
            continue
        rows[iris] = (
            iris,
            commune,
            (p.get("nom_iris") or "").strip(),
            (p.get("type_iris") or "").strip(),
            json.dumps(geom, separators=(",", ":")),
        )
    return list(rows.values()), bad


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
    IF to_regclass('quartier') IS NULL THEN
        RAISE EXCEPTION 'table quartier not found: start the Go server once to run the migrations';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM commune) THEN
        RAISE EXCEPTION 'table commune is empty: run scripts/import_communes_csv.py first';
    END IF;
END $$
"""

CREATE_TMP = """
CREATE TEMP TABLE tmp_quartier (
    iris TEXT, code_commune TEXT, nom_iris TEXT, type_iris TEXT, geom TEXT
) ON COMMIT DROP
"""

# Returns (upserted, skipped because the commune is unknown).
UPSERT = """
WITH u AS (
    INSERT INTO quartier (iris, code_commune, nom_iris, type_iris, contour)
    SELECT t.iris, t.code_commune, t.nom_iris, t.type_iris,
           ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON(t.geom), 4326))
    FROM tmp_quartier t
    JOIN commune c ON c.code_commune = t.code_commune
    ON CONFLICT (iris) DO UPDATE SET
        code_commune = EXCLUDED.code_commune,
        nom_iris     = EXCLUDED.nom_iris,
        type_iris    = EXCLUDED.type_iris,
        contour      = EXCLUDED.contour
    RETURNING 1
)
SELECT (SELECT count(*) FROM u),
       (SELECT count(*) FROM tmp_quartier) - (SELECT count(*) FROM u)
"""


def write_db(dsn: str, rows: list[Row]) -> tuple[int, int]:
    with psycopg.connect(dsn) as conn, conn.cursor() as cur:
        try:
            cur.execute(CHECK)
        except psycopg.errors.RaiseException as e:
            sys.exit(e.diag.message_primary)
        cur.execute(CREATE_TMP)
        with cur.copy("COPY tmp_quartier FROM STDIN") as copy:
            for row in tqdm(rows, desc="Sending", unit="row", unit_scale=True):
                copy.write_row(row)
        cur.execute(UPSERT)
        return tuple(cur.fetchone())


def write_psql(cmd: str, rows: list[Row]) -> tuple[int, int]:
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
COPY tmp_quartier FROM STDIN;
{data}\\.
{UPSERT};
COMMIT;
""",
    )
    upserted, skipped = (int(n) for n in fields)
    return upserted, skipped


def save(path: Path, features: list[dict]) -> None:
    opener = gzip.open if path.suffix == ".gz" else open
    data = {
        "type": "FeatureCollection",
        "numberMatched": len(features),
        "numberReturned": len(features),
        "crs": {"type": "name", "properties": {"name": "urn:ogc:def:crs:EPSG::4326"}},
        "features": features,
    }
    with opener(path, "wt", encoding="utf-8") as f:
        json.dump(data, f, separators=(",", ":"))
    print(f"Saved {len(features)} features to {path}")


def main() -> None:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "geojson", type=Path, nargs="*",
        help="contours IRIS GeoJSON file(s); downloaded from the WFS when omitted",
    )
    parser.add_argument("--url", default=WFS_URL, help="WFS GetFeature URL (GeoJSON output)")
    parser.add_argument("--save", type=Path, metavar="FILE", help="write the merged features to FILE")
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
    if args.geojson:
        features = [f for path in args.geojson for f in read_file(path)]
    else:
        features = download(args.url)
    if args.save:
        save(args.save, features)

    rows, bad = extract(features)
    print(f"{len(rows)} quartiers read; skipped {bad} features without geometry or code")

    if args.dry_run:
        for row in rows[:5]:
            print("  ", *row[:4], row[4][:60] + "…")
        return

    if args.psql:
        upserted, skipped = write_psql(args.psql, rows)
    else:
        upserted, skipped = write_db(args.dsn or default_dsn(), rows)
    print(
        f"Upserted {upserted} quartiers, skipped {skipped} (commune not in commune) "
        f"({time.monotonic() - t0:.1f}s)"
    )


if __name__ == "__main__":
    main()
