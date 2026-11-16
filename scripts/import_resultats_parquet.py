#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["pyarrow>=15", "psycopg[binary]>=3.1"]
# ///
"""Upsert the data.education.gouv.fr exam results into the `resultat` table.

Sources (Parquet exports):
  - fr-en-indicateurs-de-resultat-des-lycees-gt_v2   (bac général et techno, 2012-)
  - fr-en-indicateurs-de-resultat-des-lycees-pro_v2  (bac professionnel, 2012-)
  - fr-en-indicateurs-valeur-ajoutee-colleges        (brevet, 2022-)
  - fr-en-dnb-par-etablissement                      (brevet, 2006-2021)

Usage:
    uv run scripts/import_resultats_parquet.py [--lycees FILE] [--lycees-pro FILE] \
        [--colleges FILE] [--dnb FILE] [--dsn URL] [--dry-run]

Each source not given as a local file is downloaded from data.education.gouv.fr. The DSN defaults to the POSTGRES_* variables
(environment, then ./.env), the same ones the Go server uses. Import the
établissements first (scripts/import_etablissements_parquet.py): results whose
UAI is not in `etablissement` are skipped (foreign key).

One row per (diplome, annee, établissement):
  - lycées: one row per bac series with candidates ("Baccalauréat général",
    "Baccalauréat STMG", "Baccalauréat S"...). `admis` and `mentions` are derived
    from the published rates (presents x taux / 100), except the mentions of the
    bac général, taken from the published mention counts when available.
  - lycées pro: one "Baccalauréat professionnel" row (all domains together;
    the per-domain columns overlap). `admis` / `mentions` are derived from the
    total rates, except the mentions taken from the published counts (2022-).
  - collèges 2022-: "Brevet" (série générale, with the published mention count)
    and "Brevet série professionnelle" (no mention data). `admis` is derived
    from the success rate.
  - brevet 2006-2021: one "Brevet" row (all series together) with the exact
    présents / admis / mentions counts. It also covers lycées, EREA... that
    hold the brevet.
Rows are keyed by UNIQUE (diplome, annee, etablissement_uai): existing rows are
updated, new ones inserted. Rows are never deleted.
"""

from __future__ import annotations

import argparse
import math
import os
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

import psycopg
import pyarrow.parquet as pq

EXPORT_URL = "https://data.education.gouv.fr/api/explore/v2.1/catalog/datasets/{}/exports/parquet"
LYCEES_DATASET = "fr-en-indicateurs-de-resultat-des-lycees-gt_v2"
LYCEES_PRO_DATASET = "fr-en-indicateurs-de-resultat-des-lycees-pro_v2"
COLLEGES_DATASET = "fr-en-indicateurs-valeur-ajoutee-colleges"
DNB_DATASET = "fr-en-dnb-par-etablissement"

# Bac series of the lycées GT dataset -> `diplome` label.
BAC_SERIES = {
    "gnle": "Baccalauréat général",
    "l": "Baccalauréat L",
    "es": "Baccalauréat ES",
    "s": "Baccalauréat S",
    "sti2d": "Baccalauréat STI2D",
    "std2a": "Baccalauréat STD2A",
    "stmg": "Baccalauréat STMG",
    "stl": "Baccalauréat STL",
    "st2s": "Baccalauréat ST2S",
    "s2tmd": "Baccalauréat S2TMD",
    "sthr": "Baccalauréat STHR",
}
GNLE_MENTIONS = ("nb_mentions_tb_avecf_g", "nb_mentions_tb_sansf_g", "nb_mentions_b_g", "nb_mentions_ab_g")

BAC_PRO = "Baccalauréat professionnel"
BAC_PRO_MENTIONS = ("nb_mentions_tb_sansf_p", "nb_mentions_b_p", "nb_mentions_ab_p")

BREVET = "Brevet"
BREVET_PRO = "Brevet série professionnelle"
BREVET_MENTIONS = ("nb_mentions_ab_g", "nb_mentions_b_g", "nb_mentions_tb_g")


def rate(v) -> float | None:
    """Rates are published as numbers or strings ('98'); None if missing."""
    if v is None:
        return None
    try:
        f = float(v)
    except (TypeError, ValueError):
        return None
    return f if math.isfinite(f) else None


def from_rate(presents: int, taux) -> int | None:
    t = rate(taux)
    return None if t is None else round(presents * t / 100)


def year(v) -> int:
    """`annee` / `session` are dates (YYYY-01-01) in the Parquet export."""
    return v.year if hasattr(v, "year") else int(str(v)[:4])


def uai_of(v) -> str:
    return (v or "").strip().upper()


def extract_lycees(path: Path) -> list[tuple]:
    cols = ["annee", "uai", *GNLE_MENTIONS]
    for s in BAC_SERIES:
        cols += [f"presents_{s}", f"taux_reu_{s}", f"taux_men_{s}"]
    rows = []
    for r in pq.read_table(path, columns=cols).to_pylist():
        uai = uai_of(r["uai"])
        if not uai or r["annee"] is None:
            continue
        annee = year(r["annee"])
        for s, diplome in BAC_SERIES.items():
            presents = r[f"presents_{s}"]
            if not presents:
                continue
            mentions = from_rate(presents, r[f"taux_men_{s}"])
            if s == "gnle" and all(r[k] is not None for k in GNLE_MENTIONS):
                mentions = sum(r[k] for k in GNLE_MENTIONS)
            rows.append((diplome, annee, presents, from_rate(presents, r[f"taux_reu_{s}"]), mentions, uai))
    return rows


def extract_lycees_pro(path: Path) -> list[tuple]:
    cols = ["annee", "uai", "presents_total", "taux_reu_total", "taux_men_total", *BAC_PRO_MENTIONS]
    rows = []
    for r in pq.read_table(path, columns=cols).to_pylist():
        uai = uai_of(r["uai"])
        if not uai or r["annee"] is None or not r["presents_total"]:
            continue
        presents = round(r["presents_total"])  # published as a double
        mentions = from_rate(presents, r["taux_men_total"])
        if all(r[k] is not None for k in BAC_PRO_MENTIONS):
            mentions = sum(r[k] for k in BAC_PRO_MENTIONS)
        rows.append((BAC_PRO, year(r["annee"]), presents, from_rate(presents, r["taux_reu_total"]), mentions, uai))
    return rows


def extract_dnb(path: Path) -> list[tuple]:
    cols = ["session", "numero_d_etablissement", "presents", "admis", "admis_sans_mention"]
    rows = []
    for r in pq.read_table(path, columns=cols).to_pylist():
        uai = uai_of(r["numero_d_etablissement"])
        if not uai or r["session"] is None or not r["presents"]:
            continue
        admis = r["admis"]
        mentions = None if admis is None or r["admis_sans_mention"] is None else admis - r["admis_sans_mention"]
        rows.append((BREVET, year(r["session"]), r["presents"], admis, mentions, uai))
    return rows


def extract_colleges(path: Path) -> list[tuple]:
    cols = ["session", "uai", "nb_candidats_g", "taux_de_reussite_g", "nb_mentions_global_g",
            *BREVET_MENTIONS, "nb_candidats_p", "taux_de_reussite_p"]
    rows = []
    for r in pq.read_table(path, columns=cols).to_pylist():
        uai = uai_of(r["uai"])
        if not uai or r["session"] is None:
            continue
        annee = year(r["session"])
        if presents := r["nb_candidats_g"]:
            mentions = r["nb_mentions_global_g"]
            if mentions is None and all(r[k] is not None for k in BREVET_MENTIONS):
                mentions = sum(r[k] for k in BREVET_MENTIONS)
            rows.append((BREVET, annee, presents, from_rate(presents, r["taux_de_reussite_g"]), mentions, uai))
        if presents := r["nb_candidats_p"]:
            rows.append((BREVET_PRO, annee, presents, from_rate(presents, r["taux_de_reussite_p"]), None, uai))
    return rows


def dedupe(rows: list[tuple]) -> list[tuple]:
    """Keep the last row per (diplome, annee, uai): ON CONFLICT can't hit a row twice."""
    return list({(r[0], r[1], r[5]): r for r in rows}.values())


def download(dataset: str, dest: Path) -> Path:
    path = dest / f"{dataset}.parquet"
    print(f"Downloading {dataset}...")
    urllib.request.urlretrieve(EXPORT_URL.format(dataset), path)
    return path


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
INSERT INTO resultat (diplome, annee, presents, admis, mentions, etablissement_uai)
SELECT t.diplome, t.annee, t.presents, t.admis, t.mentions, t.uai
FROM tmp_resultat t
JOIN etablissement e ON e."UAI" = t.uai
ON CONFLICT (diplome, annee, etablissement_uai) DO UPDATE SET
    presents = EXCLUDED.presents,
    admis    = EXCLUDED.admis,
    mentions = EXCLUDED.mentions
RETURNING (xmax = 0) AS inserted
"""


def write_db(dsn: str, rows: list[tuple]) -> tuple[int, int, int]:
    with psycopg.connect(dsn) as conn, conn.cursor() as cur:
        cur.execute("SELECT to_regclass('resultat'), to_regclass('etablissement')")
        if None in cur.fetchone():
            sys.exit("tables not found: start the Go server once to run the migrations")
        cur.execute("""
            CREATE TEMP TABLE tmp_resultat (
                diplome TEXT, annee INTEGER, presents INTEGER, admis INTEGER, mentions INTEGER, uai TEXT
            ) ON COMMIT DROP
        """)
        with cur.copy("COPY tmp_resultat FROM STDIN") as copy:
            for row in rows:
                copy.write_row(row)
        cur.execute(UPSERT)
        flags = [r[0] for r in cur.fetchall()]
        inserted = sum(flags)
        return inserted, len(flags) - inserted, len(rows) - len(flags)


# (CLI option, dataset) in import order.
SOURCES = [
    ("lycees", LYCEES_DATASET),
    ("lycees-pro", LYCEES_PRO_DATASET),
    ("colleges", COLLEGES_DATASET),
    ("dnb", DNB_DATASET),
]
EXTRACTORS = {
    LYCEES_DATASET: extract_lycees,
    LYCEES_PRO_DATASET: extract_lycees_pro,
    COLLEGES_DATASET: extract_colleges,
    DNB_DATASET: extract_dnb,
}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    for opt, dataset in SOURCES:
        parser.add_argument(f"--{opt}", type=Path, help=f"{dataset} Parquet file (default: download)")
    parser.add_argument("--dsn", help="PostgreSQL URL (default: built from POSTGRES_* / .env)")
    parser.add_argument("--dry-run", action="store_true", help="parse and report, do not touch the database")
    args = parser.parse_args()

    t0 = time.monotonic()
    extracted = {}
    with tempfile.TemporaryDirectory(prefix="douceville-") as tmp:
        for opt, dataset in SOURCES:
            path = getattr(args, opt.replace("-", "_")) or download(dataset, Path(tmp))
            extracted[dataset] = EXTRACTORS[dataset](path)
            print(f"  {dataset}: {len(extracted[dataset])} rows")
    rows = dedupe([row for part in extracted.values() for row in part])
    print(f"{len(rows)} résultats to upsert")

    if args.dry_run:
        for part in extracted.values():
            for row in part[:2]:
                print("  ", row)
        return

    inserted, updated, unknown = write_db(args.dsn or default_dsn(), rows)
    print(f"Inserted {inserted}, updated {updated} rows, skipped {unknown} with unknown UAI "
          f"in {time.monotonic() - t0:.1f}s")


if __name__ == "__main__":
    main()
