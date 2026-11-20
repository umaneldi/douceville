#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["pyproj>=3.6", "psycopg[binary]>=3.1", "tqdm>=4.66"]
# ///
"""Load the RPLS "données détaillées au logement" CSV (répertoire des
logements locatifs des bailleurs sociaux, data.gouv.fr) into the
`logement_social` table.

Usage:
    uv run scripts/import_logement_social.py \
        Donnees-detaillees-au-logement-...-2025-01.csv \
        [--dsn URL | --psql CMD] [--dry-run]

To import into the docker-compose database without a direct connection:
    uv run scripts/import_logement_social.py FILE.csv \
        --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the data is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses. Run the server once beforehand so that the
migrations have created the table, and load the `quartier` table first
(scripts/import_contours_iris.py): `logement_social.iris` references it.

Columns:
    PLG_IRIS2024_CODE -> iris (rows whose code is empty or not all digits,
                         e.g. fictitious "XXXX" IRIS or Corsica, are skipped;
                         so are rows whose code is not in `quartier`)
    DROIT_LIBELLE     -> type_droit_immobilier
    TYPECONST_LIBELLE -> type_construction (NC -> NULL)
    NBPIECE           -> nb_pieces (NC -> NULL)
    SURFHAB           -> surface (more than 4 digits -> NULL)
    CONSTRUCT         -> annee_construction (<= 1000 -> NULL)
    X, Y, EPSG        -> position (POINT(lon lat) in WGS84, SRID 0 like
                         etablissement.position; NULL if missing)

The table is fully replaced. Everything runs in a single transaction (nothing
changes if the import fails).
"""

from __future__ import annotations

import argparse
import csv
import os
import shlex
import subprocess
import sys
import time
from collections import Counter, defaultdict
from collections.abc import Iterator
from pathlib import Path

import psycopg
from pyproj import Transformer
from tqdm import tqdm

BATCH_SIZE = 100_000

_transformers: dict[str, Transformer] = {}


def transformer(epsg: str) -> Transformer:
    """Transformer from EPSG:<epsg> to WGS84, (x, y) -> (lon, lat)."""
    t = _transformers.get(epsg)
    if t is None:
        t = _transformers[epsg] = Transformer.from_crs(
            f"EPSG:{epsg}", "EPSG:4326", always_xy=True
        )
    return t


def nullable(v: str) -> str | None:
    v = v.strip()
    return None if v == "" or v.upper() == "NC" else v


def to_int(v: str) -> int | None:
    v = v.strip()
    return int(v) if v.isdigit() else None


def to_float(v: str) -> float | None:
    try:
        return float(v.replace(",", "."))
    except ValueError:
        return None


def surface(v: str) -> int | None:
    """SURFHAB in m²; NULL when not a number or more than 4 digits."""
    f = to_float(v.strip())
    if f is None or f < 0 or f >= 10_000:
        return None
    return round(f)


def annee(v: str) -> int | None:
    y = to_int(v)
    return y if y is not None and y > 1000 else None


def parse(r: dict, stats: Counter) -> tuple | None:
    """The table row for a CSV row (position still as raw X, Y, EPSG), else None."""
    iris = r["PLG_IRIS2024_CODE"].strip()
    if not iris.isdigit():
        stats["bad_iris"] += 1
        return None
    x, y, epsg = to_float(r["X"]), to_float(r["Y"]), r["EPSG"].strip()
    if x is None or y is None or not epsg.isdigit():
        x = y = epsg = None
        stats["no_xy"] += 1
    return (
        iris,
        nullable(r["DROIT_LIBELLE"]),
        nullable(r["TYPECONST_LIBELLE"]),
        to_int(r["NBPIECE"]),
        surface(r["SURFHAB"]),
        annee(r["CONSTRUCT"]),
        x,
        y,
        epsg,
    )


def project(rows: list[tuple]) -> list[tuple]:
    """Replace the trailing (x, y, epsg) of each row with the WKT of the WGS84 point."""
    by_epsg: dict[str, list[int]] = defaultdict(list)
    for i, row in enumerate(rows):
        if row[-1] is not None:
            by_epsg[row[-1]].append(i)
    points: list[str | None] = [None] * len(rows)
    for epsg, idx in by_epsg.items():
        lon, lat = transformer(epsg).transform(
            [rows[i][-3] for i in idx], [rows[i][-2] for i in idx]
        )
        for i, la, lo in zip(idx, lat, lon):
            points[i] = f"POINT({lo:.7f} {la:.7f})"
    return [row[:-3] + (p,) for row, p in zip(rows, points)]


def extract(path: Path, stats: Counter, bar: tqdm) -> Iterator[list[tuple]]:
    """Yield the kept rows in batches; count read/skipped rows in stats."""
    batch: list[tuple] = []
    with open(path, "rb") as raw:
        f = (line.decode("utf-8") for line in raw)
        reader = csv.DictReader(f, delimiter=";")
        last_pos = 0
        for r in reader:
            stats["lines"] += 1
            row = parse(r, stats)
            if row is not None:
                batch.append(row)
            if len(batch) >= BATCH_SIZE:
                yield project(batch)
                batch = []
                pos = raw.tell()
                bar.update(pos - last_pos)
                last_pos = pos
        if batch:
            yield project(batch)
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
CREATE TEMP TABLE tmp_logement_social (
    iris TEXT, type_droit_immobilier TEXT, type_construction TEXT,
    nb_pieces INTEGER, surface INTEGER, annee_construction INTEGER,
    position TEXT
) ON COMMIT DROP
"""

CLEAR = "TRUNCATE logement_social"

# Run once per batch. Returns (read, inserted) for the batch; the other rows
# have an iris not in quartier.
INSERT = """
WITH r AS (
    INSERT INTO logement_social (iris, type_droit_immobilier, type_construction,
                                 nb_pieces, surface, annee_construction, position)
    SELECT t.iris, t.type_droit_immobilier, t.type_construction,
           t.nb_pieces, t.surface, t.annee_construction, ST_GeomFromText(t.position)
    FROM tmp_logement_social t
    JOIN quartier q ON q.iris = t.iris
    RETURNING 1
)
SELECT (SELECT count(*) FROM tmp_logement_social), (SELECT count(*) FROM r)
"""

CHECK = """
DO $$ BEGIN
    IF to_regclass('logement_social') IS NULL THEN
        RAISE EXCEPTION 'table logement_social not found: start the Go server once to run the migrations';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM quartier) THEN
        RAISE EXCEPTION 'table quartier is empty: run scripts/import_contours_iris.py first';
    END IF;
END $$
"""


def write_db(dsn: str, batches: Iterator[list[tuple]]) -> tuple[int, int]:
    totals = [0, 0]
    with psycopg.connect(dsn) as conn, conn.cursor() as cur:
        try:
            cur.execute(CHECK)
        except psycopg.errors.RaiseException as e:
            sys.exit(e.diag.message_primary)
        cur.execute(CREATE_TMP)
        cur.execute(CLEAR)
        for batch in batches:
            with cur.copy("COPY tmp_logement_social FROM STDIN") as copy:
                for row in batch:
                    copy.write_row(row)
            cur.execute(INSERT)
            totals = [t + n for t, n in zip(totals, cur.fetchone())]
            cur.execute("TRUNCATE tmp_logement_social")
    return tuple(totals)


def write_psql(cmd: str, batches: Iterator[list[tuple]], bar: tqdm) -> tuple[int, int]:
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
            f"BEGIN;\n{CHECK};\n{CREATE_TMP};\n{CLEAR};\n"
        )
        for batch in batches:
            proc.stdin.write("COPY tmp_logement_social FROM STDIN;\n")
            proc.stdin.writelines(
                "\t".join(map(copy_field, row)) + "\n" for row in batch
            )
            proc.stdin.write(f"\\.\n{INSERT};\nTRUNCATE tmp_logement_social;\n")
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
        "csv", type=Path, help="RPLS 'données détaillées au logement' CSV from data.gouv.fr"
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
    stats: Counter = Counter()
    with tqdm(
        total=args.csv.stat().st_size,
        unit="B",
        unit_scale=True,
        desc="Reading" if args.dry_run else "Importing",
    ) as bar:
        batches = extract(args.csv, stats, bar)
        if args.dry_run:
            kept = 0
            for batch in batches:
                for row in batch[: max(0, 5 - kept)]:
                    tqdm.write(f"   {row}")
                kept += len(batch)
        elif args.psql:
            kept, inserted = write_psql(args.psql, batches, bar)
        else:
            kept, inserted = write_db(args.dsn or default_dsn(), batches)

    print(
        f"{stats['lines']} lines read; {kept} kept, {stats['bad_iris']} skipped "
        f"(empty or non-numeric IRIS); {stats['no_xy']} kept without coordinates"
    )
    if not args.dry_run:
        print(
            f"inserted {inserted}, skipped {kept - inserted} (iris not in quartier) "
            f"in {time.monotonic() - t0:.1f}s"
        )


if __name__ == "__main__":
    main()
