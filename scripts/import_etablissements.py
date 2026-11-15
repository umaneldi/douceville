#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["rdflib>=7", "psycopg[binary]>=3.1"]
# ///
"""Import the Eurecom "écoles" Turtle dataset into the `etablissement` table.

Usage:
    uv run scripts/import_etablissements.py dataset-564055.ttl [--dsn URL] [--dry-run]

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses. Run the server once beforehand so that the
migrations have created the table.

The dataset describes each school (keyed by its UAI code) in two shapes:
  * a "full" record: title, ecole:nature (natureUAI code), denominationPrincipale,
    ecole:etat, and a blank-node projected point (see PROJECTED_SRID);
  * a "light" record: rdfs:label plus a geometry/<uai> node with WGS84 lon/lat.
Both are merged per UAI. WGS84 coordinates are preferred; otherwise the Lambert
point is reprojected by PostGIS. Positions are stored with SRID 0 (see schema.md).

The dataset has no address, postal code or commune: `adresse`, `lieu_dit`,
`academie` and `ouverture` are left NULL, `code_postal` and `commune` (NOT NULL)
are set to ''. Existing rows with the same UAI are updated, but a non-empty
code_postal/commune already present in the database is kept.
"""

from __future__ import annotations

import argparse
import math
import os
import sys
import time
import unicodedata
from pathlib import Path

import psycopg
from rdflib import BNode, Graph, Namespace, URIRef
from rdflib.namespace import DCTERMS, RDFS

AIISO = Namespace("http://purl.org/vocab/aiiso/schema#")
ECOLE = Namespace("http://data.eurecom.fr/ontologies/ecole#")
GEOM = Namespace("http://data.ign.fr/ontologies/geometrie#")

SCHOOL_PREFIX = "http://data.eurecom.fr/id/school/"
GEOMETRY_PREFIX = SCHOOL_PREFIX + "geometry/"
NATURE_PREFIX = "http://data.eurecom.fr/codes/ecole/natureUAI/"

# Values expected by the frontend filters (web/index.html).
MATERNELLE, ELEMENTAIRE, COLLEGE, LYCEE = "Maternelle", "Elementaire", "Collège", "Lycée"

# UAI department prefixes that do not map to "drop the leading zero".
DEPT_OVERRIDES = {"620": "2A", "720": "2B"}

# CRS of the projected (blank-node) coordinates. Although tagged "ntflamb2e",
# metropolitan points are Lambert-93 and overseas ones use local UTM zones.
PROJECTED_SRID = {"971": 5490, "972": 5490, "973": 2972, "974": 2975}  # default 2154


def strip_accents(s: str) -> str:
    return "".join(c for c in unicodedata.normalize("NFD", s) if unicodedata.category(c) != "Mn")


def departement_from_uai(uai: str) -> str:
    prefix = uai[:3]
    if prefix in DEPT_OVERRIDES:
        return DEPT_OVERRIDES[prefix]
    if prefix.startswith("0"):
        return prefix[1:]
    return prefix  # DOM: 971, 972, ...


def nature_from_code(code: int | None, denomination: str) -> str | None:
    """Map a natureUAI code (+ denomination) to the app's nature values."""
    if code is None:
        return None
    if 100 <= code < 200:  # premier degré
        if code in (101, 102, 103) or "MATERNELLE" in denomination:
            return MATERNELLE
        return ELEMENTAIRE  # élémentaire, primaire, spécialisée...
    if 300 <= code < 340:  # lycées et sections de lycée (SEP, SGT)
        return LYCEE
    if 340 <= code < 400:  # collèges, SEGPA, EREA
        return COLLEGE
    return None


def nature_from_text(text: str) -> str | None:
    """Fallback for records without a natureUAI code: guess from the name."""
    t = strip_accents(text).upper()
    if "MATERNELLE" in t:
        return MATERNELLE
    if "LYCEE" in t or t.startswith(("LP ", "LPO ", "LGT ")):
        return LYCEE
    if "COLLEGE" in t or "SEGPA" in t:
        return COLLEGE
    if "ELEMENTAIRE" in t or "PRIMAIRE" in t or t.startswith("ECOLE"):
        return ELEMENTAIRE
    return None


def to_float(value) -> float | None:
    try:
        f = float(str(value))
    except (TypeError, ValueError):
        return None
    return f if math.isfinite(f) else None


def to_int(value) -> int | None:
    try:
        return int(str(value))
    except (TypeError, ValueError):
        return None  # a few records have shifted/corrupted fields


def first(g: Graph, s, p):
    return next(g.objects(s, p), None)


def extract(g: Graph, keep_all_etats: bool):
    """Yield one row per UAI and return skip statistics."""
    stats = {"no_position": 0, "no_nature": 0, "etat": 0}
    schools: dict[str, dict] = {}

    for s, code in g.subject_objects(AIISO.code):
        if not isinstance(s, URIRef) or not str(s).startswith(SCHOOL_PREFIX):
            continue
        uai = str(code).strip().upper()
        rec = schools.setdefault(uai, {"subjects": []})
        rec["subjects"].append(s)

    rows = []
    for uai, rec in schools.items():
        title = label = denomination = None
        nature_code = etat = None
        lambert = None
        for s in rec["subjects"]:
            title = title or first(g, s, DCTERMS.title)
            label = label or first(g, s, RDFS.label)
            denomination = denomination or first(g, s, ECOLE.denominationPrincipale)
            n = first(g, s, ECOLE.nature)
            if n is not None and str(n).startswith(NATURE_PREFIX):
                nature_code = to_int(str(n)[len(NATURE_PREFIX):])
            etat = etat if etat is not None else to_int(first(g, s, ECOLE.etat))
            pt = first(g, s, GEOM.geometrie)
            if isinstance(pt, BNode):
                x, y = to_float(first(g, pt, GEOM.coordX)), to_float(first(g, pt, GEOM.coordY))
                if x is not None and y is not None:
                    lambert = (x, y)

        # ecole:etat: 1 = ouvert. Other states are skipped unless asked otherwise.
        if etat is not None and etat != 1 and not keep_all_etats:
            stats["etat"] += 1
            continue

        geo = URIRef(GEOMETRY_PREFIX + uai.lower())
        lon, lat = to_float(first(g, geo, GEOM.coordX)), to_float(first(g, geo, GEOM.coordY))
        if lon is None or lat is None:
            lon = lat = None
            if lambert is None:
                stats["no_position"] += 1
                continue

        denomination = str(denomination or "")
        nom = str(title or label or "").strip()
        if not nom:
            patronyme = first(g, rec["subjects"][0], ECOLE.patronyme)
            nom = f"{denomination} {patronyme or ''}".strip() or uai

        nature = nature_from_code(nature_code, denomination) or nature_from_text(f"{denomination} {nom}")
        if nature is None:
            stats["no_nature"] += 1
            continue

        prive = "PRIV" in strip_accents(f"{denomination} {nom}").upper()
        rows.append((
            uai, nom, departement_from_uai(uai), "privé" if prive else "public", nature,
            lon, lat, *(lambert or (None, None)), PROJECTED_SRID.get(uai[:3], 2154),
        ))
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


def default_dsn() -> str:
    load_dotenv(Path.cwd() / ".env")
    env = os.environ.get
    return (
        f"postgresql://{env('POSTGRES_USER', 'douceville')}:{env('POSTGRES_PASSWORD', '')}"
        f"@{env('POSTGRES_HOST', 'localhost')}:{env('POSTGRES_PORT', '5432')}"
        f"/{env('POSTGRES_DB', 'douceville')}"
    )


UPSERT = """
INSERT INTO etablissement ("UAI", nom, code_postal, commune, position, departement, secteur, nature)
SELECT uai, nom, '', '',
       ST_SetSRID(
         CASE WHEN lon IS NOT NULL THEN ST_MakePoint(lon, lat)
              ELSE ST_Transform(ST_SetSRID(ST_MakePoint(lx, ly), srid), 4326) END,
         0),
       departement, secteur, nature
FROM tmp_etab
ON CONFLICT ("UAI") DO UPDATE SET
    nom         = EXCLUDED.nom,
    position    = EXCLUDED.position,
    departement = EXCLUDED.departement,
    secteur     = EXCLUDED.secteur,
    nature      = EXCLUDED.nature
"""


def write_db(dsn: str, rows: list[tuple]) -> int:
    with psycopg.connect(dsn) as conn, conn.cursor() as cur:
        cur.execute("SELECT to_regclass('etablissement')")
        if cur.fetchone()[0] is None:
            sys.exit("table 'etablissement' not found: start the Go server once to run the migrations")
        cur.execute("""
            CREATE TEMP TABLE tmp_etab (
                uai TEXT, nom TEXT, departement TEXT, secteur TEXT, nature TEXT,
                lon DOUBLE PRECISION, lat DOUBLE PRECISION,
                lx DOUBLE PRECISION, ly DOUBLE PRECISION, srid INTEGER
            ) ON COMMIT DROP
        """)
        with cur.copy("COPY tmp_etab FROM STDIN") as copy:
            for row in rows:
                copy.write_row(row)
        cur.execute(UPSERT)
        return cur.rowcount


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("ttl", type=Path, help="Turtle file (e.g. dataset-564055.ttl)")
    parser.add_argument("--dsn", help="PostgreSQL URL (default: built from POSTGRES_* / .env)")
    parser.add_argument("--keep-all-etats", action="store_true", help="also import schools whose ecole:etat != 1")
    parser.add_argument("--dry-run", action="store_true", help="parse and report, do not touch the database")
    args = parser.parse_args()

    t0 = time.monotonic()
    print(f"Parsing {args.ttl} (this takes a few minutes)...", flush=True)
    g = Graph()
    g.parse(args.ttl, format="turtle")
    print(f"  {len(g)} triples in {time.monotonic() - t0:.0f}s", flush=True)

    rows, stats = extract(g, args.keep_all_etats)
    del g
    print(f"  {len(rows)} établissements to import; skipped: "
          f"{stats['etat']} not open (etat != 1), {stats['no_position']} without position, "
          f"{stats['no_nature']} with unknown nature")

    if args.dry_run:
        for row in rows[:5]:
            print("  ", row)
        return

    n = write_db(args.dsn or default_dsn(), rows)
    print(f"Upserted {n} rows into etablissement in {time.monotonic() - t0:.0f}s total")


if __name__ == "__main__":
    main()
