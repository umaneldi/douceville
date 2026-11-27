#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["pyarrow>=15", "psycopg[binary]>=3.1", "tqdm>=4.66"]
# ///
"""Upsert the "annuaire de l'éducation" Parquet export (data.education.gouv.fr,
dataset fr-en-annuaire-education, mirrored on data.gouv.fr) into the
`etablissement` table.

Usage:
    uv run scripts/import_etablissements_parquet.py \
        .parquet/annuaire-de-leducation.parquet \
        [--dsn URL | --psql CMD] [--dry-run]

To import into the docker-compose database without a direct connection:
    uv run scripts/import_etablissements_parquet.py FILE.parquet \
        --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the data is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses. Run the server once beforehand so that the
migrations have created the table and its columns (up to
021_etablissement_annuaire.sql).

Rows are keyed by UAI (`Identifiant_de_l_etablissement`): existing rows are
updated, new ones inserted. Rows are never deleted. A UAI listed several times
(several sites of one établissement) is merged: text and position from its
first row, yes/no flags OR-ed, sections united. Closed rows (`etat` FERME) are
skipped. WGS84 latitude/longitude are preferred; otherwise the projected
coordX/Y_origine are reprojected by PostGIS using the row's `epsg_origine`.
Rows without any position, or whose nature does not map to one of the app's
values (Maternelle, Elementaire, Collège, Lycée), are skipped.
An école that is both maternelle and élémentaire (`Ecole_maternelle` and
`Ecole_elementaire`), or whose name contains "primaire" (case-insensitive, same
rule as migration 019_etablissement_primaire.sql), gets the nature 'Primaire':
it matches both the Maternelle and Elementaire search criteria.
`secteur` is 'public', 'privé' (sous contrat) or 'hors contrat', the latter
taken from `Type_contrat_prive`.
`filieres` lists the filières of a lycée (several possible, empty for other
natures): from the `Voie_generale` / `Voie_technologique` /
`Voie_professionnelle` flags when at least one is true, otherwise from its
natureUAI code: 300 LEGT and 335 section GT -> Général + Technologique; 301 ->
Technologique; 302, 310 (climatique), 315 (expérimental) -> Général; 306
polyvalent and 307 LEGTPA -> Général + Technologique + Professionnel; 320 LP,
332, 334 SEP -> Professionnel. Agricole is added when `Lycee_Agricole` is set
or the ministry (`ministere_tutelle`) is AGRICULTURE. The column comes from
migration 020_etablissement_filieres.sql.
The annuaire attributes (migration 021_etablissement_annuaire.sql) are also
stored: éducation prioritaire (REP / REP+), restauration, hébergement, ULIS,
SEGPA, apprentissage, post-bac, lycée des métiers, sections (Européenne,
Internationale, Sport, Arts, Cinéma, Théâtre), phone, e-mail, website and
ONISEP page. `lieu_dit` is `Adresse_2` unless it is a PO box (BP, CS, TSA).
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
import re
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
# Filières of a lycée (models.Filieres in the Go code).
GENERAL, TECHNO, PRO, AGRICOLE = "Général", "Technologique", "Professionnel", "Agricole"
FILIERES_BY_CODE = {
    300: (GENERAL, TECHNO),  # lycée d'enseignement général et technologique
    301: (TECHNO,),  # lycée d'enseignement technologique
    302: (GENERAL,),  # lycée d'enseignement général
    306: (GENERAL, TECHNO, PRO),  # lycée polyvalent
    307: (GENERAL, TECHNO, PRO),  # lycée général, techno et pro agricole
    310: (GENERAL,),  # lycée climatique
    315: (GENERAL,),  # lycée expérimental
    320: (PRO,),  # lycée professionnel
    332: (PRO,),  # école professionnelle spécialisée
    334: (PRO,),  # section d'enseignement professionnel
    335: (GENERAL, TECHNO),  # section d'enseignement général et technologique
}
MINISTERE_AGRICULTURE = "AGRICULTURE"

# Sections of the annuaire: column -> stored value.
SECTIONS = {
    "Section_europeenne": "Européenne",
    "Section_internationale": "Internationale",
    "Section_sport": "Sport",
    "Section_arts": "Arts",
    "Section_cinema": "Cinéma",
    "Section_theatre": "Théâtre",
}
# Yes/no attributes of the annuaire: column -> etablissement column.
FLAGS = {
    "Restauration": "restauration",
    "Hebergement": "hebergement",
    "ULIS": "ulis",
    "Segpa": "segpa",
    "Apprentissage": "apprentissage",
    "Post_BAC": "post_bac",
    "Lycee_des_metiers": "lycee_des_metiers",
}
VOIES = {
    "Voie_generale": GENERAL,
    "Voie_technologique": TECHNO,
    "Voie_professionnelle": PRO,
}
# A PO box in Adresse_2 is not a lieu-dit.
PO_BOX = re.compile(r"^(B\.?P\.?|CS|TSA)\b", re.IGNORECASE)

COLUMNS = [
    "Identifiant_de_l_etablissement",
    "Nom_etablissement",
    "Statut_public_prive",
    "Type_contrat_prive",
    "Adresse_1",
    "Adresse_2",
    "Code_postal",
    "Nom_commune",
    "Code_commune",
    "Code_departement",
    "Libelle_academie",
    "latitude",
    "longitude",
    "coordX_origine",
    "coordY_origine",
    "epsg_origine",
    "code_nature",
    "ministere_tutelle",
    "date_ouverture",
    "etat",
    "Ecole_maternelle",
    "Ecole_elementaire",
    "Lycee_Agricole",
    "Appartenance_Education_Prioritaire",
    "Telephone",
    "Mail",
    "Web",
    "Fiche_onisep",
    *VOIES,
    *FLAGS,
    *SECTIONS,
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


def filieres_for(r: dict, nature: str) -> list[str]:
    """Filières of a lycée from its Voie_* flags (or, when none is true, its
    natureUAI code) and its agricultural status; empty for other natures."""
    if nature != LYCEE:
        return []
    if any(r[col] for col in VOIES):
        filieres = [f for col, f in VOIES.items() if r[col]]
    else:
        filieres = list(FILIERES_BY_CODE.get(r["code_nature"], ()))
    ministere = (r["ministere_tutelle"] or "").strip().upper()
    if r["Lycee_Agricole"] or ministere == MINISTERE_AGRICULTURE:
        filieres.append(AGRICOLE)
    return filieres


def nature_for(r: dict, nom: str) -> str | None:
    """App nature of an établissement: 'Primaire' for an école both maternelle
    and élémentaire or whose name says so, otherwise mapped from its natureUAI
    code."""
    nature = nature_from_code(r["code_nature"])
    if nature in (MATERNELLE, ELEMENTAIRE) and r["Ecole_maternelle"] and r["Ecole_elementaire"]:
        return PRIMAIRE
    if "primaire" in nom.casefold():
        return PRIMAIRE
    return nature


def lieu_dit(adresse_2: str | None) -> str | None:
    """Adresse_2, unless it is empty or a PO box."""
    v = (adresse_2 or "").strip()
    return v if v and not PO_BOX.match(v) else None


def text(v: str | None) -> str | None:
    """Stripped text, None when empty."""
    v = (v or "").strip()
    return v or None


def merge(rows: list[dict]) -> dict:
    """Merge the rows of one UAI: first row, flags OR-ed (None only when all
    are None), sections united."""
    first = dict(rows[0])
    for col in (*FLAGS, *SECTIONS, *VOIES, "Lycee_Agricole", "Ecole_maternelle", "Ecole_elementaire"):
        values = [r[col] for r in rows if r[col] is not None]
        first[col] = any(values) if values else None
    return first


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
    stats = {"no_position": 0, "no_nature": 0, "no_uai": 0, "closed": 0, "merged": 0}
    by_uai: dict[str, list[dict]] = {}
    for r in tqdm(table.to_pylist(), desc="Reading", unit="row", unit_scale=True):
        uai = (r["Identifiant_de_l_etablissement"] or "").strip().upper()
        if not uai:
            stats["no_uai"] += 1
            continue
        if (r["etat"] or "").upper() == "FERME":
            stats["closed"] += 1
            continue
        by_uai.setdefault(uai, []).append(r)

    rows = []
    for uai, group in by_uai.items():
        stats["merged"] += len(group) - 1
        r = merge(group)

        lon, lat = finite(r["longitude"]), finite(r["latitude"])
        x, y = finite(r["coordX_origine"]), finite(r["coordY_origine"])
        s = srid(r["epsg_origine"])
        if lon is None or lat is None:
            lon = lat = None
            if x is None or y is None or s is None:
                stats["no_position"] += 1
                continue

        nom = (r["Nom_etablissement"] or "").strip() or uai
        nature = nature_for(r, nom)
        if nature is None:
            stats["no_nature"] += 1
            continue

        rows.append(
            (
                uai,
                nom,
                text(r["Adresse_1"]),
                lieu_dit(r["Adresse_2"]),
                r["Code_postal"] or "",
                r["Nom_commune"] or "",
                code_commune(r["Code_commune"]),
                departement(r["Code_departement"] or ""),
                r["Libelle_academie"],
                secteur(r["Statut_public_prive"], r["Type_contrat_prive"], nom),
                r["date_ouverture"],
                nature,
                filieres_for(r, nature),
                text(r["Appartenance_Education_Prioritaire"]),
                *(r[col] for col in FLAGS),
                [v for col, v in SECTIONS.items() if r[col]],
                text(r["Telephone"]),
                text(r["Mail"]),
                text(r["Web"]),
                text(r["Fiche_onisep"]),
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
    if isinstance(v, bool):
        return "t" if v else "f"
    if isinstance(v, list):  # text[] literal, e.g. {"Général","Agricole"}
        v = "{" + ",".join('"' + str(x).replace("\\", "\\\\").replace('"', '\\"') + '"' for x in v) + "}"
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
    departement TEXT, academie TEXT, secteur TEXT, ouverture DATE, nature TEXT, filieres TEXT[],
    education_prioritaire TEXT, restauration BOOLEAN, hebergement BOOLEAN, ulis BOOLEAN,
    segpa BOOLEAN, apprentissage BOOLEAN, post_bac BOOLEAN, lycee_des_metiers BOOLEAN,
    sections TEXT[], telephone TEXT, mail TEXT, site_web TEXT, fiche_onisep TEXT,
    lon DOUBLE PRECISION, lat DOUBLE PRECISION,
    x DOUBLE PRECISION, y DOUBLE PRECISION, srid INTEGER
) ON COMMIT DROP
"""

ANNUAIRE_COLS = (
    "education_prioritaire, restauration, hebergement, ulis, segpa, apprentissage, post_bac, "
    "lycee_des_metiers, sections, telephone, mail, site_web, fiche_onisep"
)
ANNUAIRE_SET = ",\n    ".join(f"{c} = EXCLUDED.{c}" for c in ANNUAIRE_COLS.split(", "))

UPSERT = f"""
INSERT INTO etablissement ("UAI", nom, adresse, lieu_dit, code_postal, commune, code_commune, position,
                           departement, academie, secteur, ouverture, nature, filieres, {ANNUAIRE_COLS})
SELECT uai, nom, adresse, lieu_dit, code_postal, commune,
       (SELECT c.code_commune FROM commune c WHERE c.code_commune = t.code_commune),
       ST_SetSRID(
         CASE WHEN lon IS NOT NULL THEN ST_MakePoint(lon, lat)
              ELSE ST_Transform(ST_SetSRID(ST_MakePoint(x, y), srid), 4326) END,
         0),
       departement, academie, secteur, ouverture, nature, filieres, {ANNUAIRE_COLS}
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
    nature      = EXCLUDED.nature,
    filieres    = EXCLUDED.filieres,
    {ANNUAIRE_SET}
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
        "parquet", type=Path, help="annuaire de l'éducation Parquet file (data.education.gouv.fr)"
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
        f"{stats['no_nature']} with unmapped nature, {stats['no_uai']} without UAI, "
        f"{stats['closed']} closed; {stats['merged']} duplicate rows merged"
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
