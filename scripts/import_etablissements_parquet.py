#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["pyarrow>=15", "psycopg[binary]>=3.1", "tqdm>=4.66"]
# ///
"""Upsert the data.education.gouv.fr "adresse et géolocalisation des
établissements" Parquet export into the `etablissement` table.

Usage:
    uv run scripts/import_etablissements_parquet.py \
        fr-en-adresse-et-geolocalisation-etablissements-premier-et-second-degre.parquet \
        [--dsn URL | --psql CMD] [--dry-run]

To import into the docker-compose database without a direct connection:
    uv run scripts/import_etablissements_parquet.py FILE.parquet \
        --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the data is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses. Run the server once beforehand so that the
migrations have created the table.

Rows are keyed by UAI (`numero_uai`): existing rows are updated, new ones
inserted. Rows are never deleted. WGS84 latitude/longitude are preferred;
otherwise the projected coordonnee_x/y are reprojected by PostGIS using the
row's `epsg`. Rows without any position, or whose nature does not map to one of
the app's values (Maternelle, Elementaire, Collège, Lycée), are skipped.
An établissement whose name contains "primaire" (case-insensitive) gets the
nature 'Primaire' whatever its natureUAI code: such a school matches both the
Maternelle and Elementaire search criteria (same rule as migration
019_etablissement_primaire.sql).
`secteur` is 'public', 'privé' (sous contrat) or 'hors contrat', the latter
taken from `secteur_prive_libelle_type_contrat`.
Positions are stored with SRID 0 (see schema.md).
`code_commune` links to the `commune` table (load it first with
scripts/import_communes_csv.py): arrondissement codes of Paris, Marseille and
Lyon are mapped to their parent commune, and codes missing from `commune`
(e.g. collectivités d'outre-mer) are stored as NULL.
"""

from __future__ import annotations

import argparse
import math
import os
import shlex
import subprocess
import sys
import time
from pathlib import Path

import psycopg
from tqdm import tqdm
import pyarrow.parquet as pq

# Values expected by the frontend filters (web/index.html).
MATERNELLE, ELEMENTAIRE, COLLEGE, LYCEE, PRIMAIRE = (
    "Maternelle",
    "Elementaire",
    "Collège",
    "Lycée",
    "Primaire",
)
PUBLIC, PRIVE, HORS_CONTRAT = "public", "privé", "hors contrat"

COLUMNS = [
    "numero_uai",
    "appellation_officielle",
    "denomination_principale",
    "patronyme_uai",
    "secteur_public_prive_libe",
    "secteur_prive_libelle_type_contrat",
    "adresse_uai",
    "lieu_dit_uai",
    "code_postal_uai",
    "libelle_commune",
    "code_commune",
    "latitude",
    "longitude",
    "coordonnee_x",
    "coordonnee_y",
    "epsg",
    "nature_uai",
    "code_departement",
    "libelle_academie",
    "date_ouverture",
]


def nature_from_code(code: int | None) -> str | None:
    """Map a natureUAI code to the app's nature values."""
    if code is None:
        return None
    if code in (101, 102, 103):  # maternelle (+ d'application)
        return MATERNELLE
    if 100 <= code < 200:  # élémentaire, primaire, spécialisée...
        return ELEMENTAIRE
    if 300 <= code < 340:  # lycées et sections de lycée (SEP, SGT)
        return LYCEE
    if 340 <= code < 400:  # collèges, SEGPA, EREA
        return COLLEGE
    return None


def nature_for(code: int | None, nom: str) -> str | None:
    """App nature of an établissement: 'Primaire' when its name says so,
    otherwise mapped from its natureUAI code."""
    if "primaire" in nom.casefold():
        return PRIMAIRE
    return nature_from_code(code)


def secteur(libe: str | None, contrat: str | None, denomination: str) -> str:
    """'public', 'privé' (sous contrat) or 'hors contrat'."""
    contrat = (contrat or "").upper()
    if contrat == "HORS CONTRAT":
        return HORS_CONTRAT
    if libe:
        return PRIVE if libe.lower().startswith("priv") else PUBLIC
    # Sector missing: a private contract type, or the name, tells it apart.
    if "CONTRAT" in contrat or "PRIV" in denomination.upper():
        return PRIVE
    return PUBLIC


def departement(code: str) -> str:
    """'051' -> '51', '02A' -> '2A', '971' -> '971' (same format as before)."""
    return code[1:] if len(code) == 3 and code.startswith("0") else code


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


def finite(v) -> float | None:
    return v if v is not None and math.isfinite(v) else None


def srid(epsg: str | None) -> int | None:
    if not epsg:
        return None
    try:
        return int(epsg.rsplit(":", 1)[-1])
    except ValueError:
        return None


def extract(path: Path):
    table = pq.read_table(path, columns=COLUMNS)
    stats = {"no_position": 0, "no_nature": 0, "no_uai": 0}
    rows = []
    for r in tqdm(table.to_pylist(), desc="Reading", unit="row", unit_scale=True):
        uai = (r["numero_uai"] or "").strip().upper()
        if not uai:
            stats["no_uai"] += 1
            continue

        lon, lat = finite(r["longitude"]), finite(r["latitude"])
        x, y, s = finite(r["coordonnee_x"]), finite(r["coordonnee_y"]), srid(r["epsg"])
        if lon is None or lat is None:
            lon = lat = None
            if x is None or y is None or s is None:
                stats["no_position"] += 1
                continue

        denomination = r["denomination_principale"] or ""
        nom = (
            (r["appellation_officielle"] or "").strip()
            or f"{denomination} {r['patronyme_uai'] or ''}".strip()
            or uai
        )

        nature = nature_for(r["nature_uai"], nom)
        if nature is None:
            stats["no_nature"] += 1
            continue

        rows.append(
            (
                uai,
                nom,
                r["adresse_uai"],
                r["lieu_dit_uai"],
                r["code_postal_uai"] or "",
                r["libelle_commune"] or "",
                code_commune(r["code_commune"]),
                departement(r["code_departement"] or ""),
                r["libelle_academie"],
                secteur(
                    r["secteur_public_prive_libe"],
                    r["secteur_prive_libelle_type_contrat"],
                    denomination,
                ),
                r["date_ouverture"],
                nature,
                lon,
                lat,
                x,
                y,
                s,
            )
        )
    return rows, stats


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
            shlex.split(cmd), input=script, text=True, stdout=subprocess.PIPE
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


CREATE_TMP = """
CREATE TEMP TABLE tmp_etab (
    uai TEXT, nom TEXT, adresse TEXT, lieu_dit TEXT, code_postal TEXT, commune TEXT, code_commune TEXT,
    departement TEXT, academie TEXT, secteur TEXT, ouverture DATE, nature TEXT,
    lon DOUBLE PRECISION, lat DOUBLE PRECISION,
    x DOUBLE PRECISION, y DOUBLE PRECISION, srid INTEGER
) ON COMMIT DROP
"""

UPSERT = """
INSERT INTO etablissement ("UAI", nom, adresse, lieu_dit, code_postal, commune, code_commune, position,
                           departement, academie, secteur, ouverture, nature)
SELECT uai, nom, adresse, lieu_dit, code_postal, commune,
       (SELECT c.code_commune FROM commune c WHERE c.code_commune = t.code_commune),
       ST_SetSRID(
         CASE WHEN lon IS NOT NULL THEN ST_MakePoint(lon, lat)
              ELSE ST_Transform(ST_SetSRID(ST_MakePoint(x, y), srid), 4326) END,
         0),
       departement, academie, secteur, ouverture, nature
FROM tmp_etab t
ON CONFLICT ("UAI") DO UPDATE SET
    nom          = EXCLUDED.nom,
    adresse      = EXCLUDED.adresse,
    lieu_dit     = EXCLUDED.lieu_dit,
    code_postal  = EXCLUDED.code_postal,
    commune      = EXCLUDED.commune,
    code_commune = EXCLUDED.code_commune,
    position    = EXCLUDED.position,
    departement = EXCLUDED.departement,
    academie    = EXCLUDED.academie,
    secteur     = EXCLUDED.secteur,
    ouverture   = EXCLUDED.ouverture,
    nature      = EXCLUDED.nature
RETURNING (xmax = 0) AS inserted
"""


def write_db(dsn: str, rows: list[tuple]) -> tuple[int, int]:
    with psycopg.connect(dsn) as conn, conn.cursor() as cur:
        cur.execute("SELECT to_regclass('etablissement')")
        if cur.fetchone()[0] is None:
            sys.exit(
                "table 'etablissement' not found: start the Go server once to run the migrations"
            )
        cur.execute(CREATE_TMP)
        with cur.copy("COPY tmp_etab FROM STDIN") as copy:
            for row in tqdm(rows, desc="Sending", unit="row", unit_scale=True):
                copy.write_row(row)
        cur.execute(UPSERT)
        flags = [r[0] for r in cur.fetchall()]
        inserted = sum(flags)
        return inserted, len(flags) - inserted


def write_psql(cmd: str, rows: list[tuple]) -> tuple[int, int]:
    """Same as write_db, through a psql command (e.g. `docker exec -i <container> psql ...`)."""
    data = "".join(
        "\t".join(map(copy_field, row)) + "\n"
        for row in tqdm(rows, desc="Serializing", unit="row", unit_scale=True)
    )
    inserted, updated = run_psql(
        cmd,
        f"""
BEGIN;
DO $$ BEGIN
    IF to_regclass('etablissement') IS NULL THEN
        RAISE EXCEPTION 'table etablissement not found: start the Go server once to run the migrations';
    END IF;
END $$;
{CREATE_TMP};
COPY tmp_etab FROM STDIN;
{data}\\.
WITH r AS ({UPSERT}) SELECT count(*) FILTER (WHERE inserted), count(*) FILTER (WHERE NOT inserted) FROM r;
COMMIT;
""",
    )
    return int(inserted), int(updated)


def main() -> None:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "parquet", type=Path, help="Parquet file from data.education.gouv.fr"
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
    rows, stats = extract(args.parquet)
    print(
        f"{len(rows)} établissements to upsert; skipped: {stats['no_position']} without position, "
        f"{stats['no_nature']} with unmapped nature, {stats['no_uai']} without UAI"
    )

    if args.dry_run:
        for row in rows[:5]:
            print("  ", row)
        return

    if args.psql:
        inserted, updated = write_psql(args.psql, rows)
    else:
        inserted, updated = write_db(args.dsn or default_dsn(), rows)
    print(
        f"Inserted {inserted}, updated {updated} rows in {time.monotonic() - t0:.1f}s"
    )


if __name__ == "__main__":
    main()
