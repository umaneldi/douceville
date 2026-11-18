#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["psycopg[binary]>=3.1"]
# ///
"""Show or change the access level (`perms`) of douceville users.

Usage:
    uv run scripts/set_user_perms.py --list [--dsn URL | --psql CMD]
    uv run scripts/set_user_perms.py EMAIL [LEVEL] [--dsn URL | --psql CMD]

Levels, in increasing order: unverified < verified < granted < admin. Only
`granted` and `admin` can use the application; the change applies to the next
request of the user (no need to log out). Without LEVEL, the user's current
level is shown. E-mails are matched case-insensitively.

To work on the docker-compose database without a direct connection, add
    --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the SQL is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses.
"""

from __future__ import annotations

import argparse
import os
import shlex
import subprocess
import sys
from pathlib import Path

LEVELS = ("unverified", "verified", "granted", "admin")
FIELDS = ("email", "perms", "first_name", "last_name")

LIST = "SELECT email, perms::text, first_name, last_name FROM utilisateur ORDER BY perms DESC, lower(email)"
SHOW = "SELECT email, perms::text, first_name, last_name FROM utilisateur WHERE lower(email) = lower({email})"
UPDATE = """
UPDATE utilisateur u SET perms = {level}::user_perms
FROM (SELECT id, perms AS old FROM utilisateur WHERE lower(email) = lower({email}) FOR UPDATE) o
WHERE u.id = o.id
RETURNING u.email, o.old::text, u.perms::text
"""


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


def literal(s: str) -> str:
    """SQL string literal (standard_conforming_strings, the PostgreSQL default)."""
    return "'" + s.replace("'", "''") + "'"


def query_psql(cmd: str, sql: str) -> list[tuple]:
    """Run one statement through the psql command `cmd`; return its rows."""
    script = (
        "\\set ON_ERROR_STOP on\n\\set QUIET on\n\\pset tuples_only on\n\\pset format unaligned\n"
        "\\pset fieldsep '\\t'\n\\pset null ''\n" + sql + ";\n"
    )
    try:
        proc = subprocess.run(
            shlex.split(cmd), input=script, text=True, stdout=subprocess.PIPE
        )
    except OSError as e:
        sys.exit(f"--psql: cannot run {cmd!r}: {e}")
    if proc.returncode != 0:
        sys.exit(f"--psql: query failed (exit code {proc.returncode})")
    return [
        tuple(f or None for f in line.split("\t"))
        for line in proc.stdout.splitlines()
        if line.strip()
    ]


def query_db(dsn: str, sql: str) -> list[tuple]:
    import psycopg

    with psycopg.connect(dsn) as conn, conn.cursor() as cur:
        cur.execute(sql)
        return cur.fetchall()


def print_users(rows: list[tuple]) -> None:
    table = [FIELDS] + [tuple(v or "" for v in r) for r in rows]
    widths = [max(len(r[i]) for r in table) for i in range(len(FIELDS))]
    for r in table:
        print("  ".join(v.ljust(w) for v, w in zip(r, widths)).rstrip())


def main() -> None:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("email", nargs="?", help="e-mail of the user")
    parser.add_argument(
        "level",
        nargs="?",
        choices=LEVELS,
        help="new access level (default: show the current one)",
    )
    parser.add_argument(
        "--list", action="store_true", help="list all users and their level"
    )
    parser.add_argument(
        "--dsn", help="PostgreSQL URL (default: built from POSTGRES_* / .env)"
    )
    parser.add_argument(
        "--psql",
        metavar="CMD",
        help="run through this psql command instead of connecting with --dsn, e.g. "
        "'docker exec -i douceville-geodb-1 psql -U douceville -d douceville'",
    )
    args = parser.parse_args()
    if args.list == bool(args.email):
        parser.error("give either --list or an EMAIL")

    def query(sql: str) -> list[tuple]:
        return (
            query_psql(args.psql, sql)
            if args.psql
            else query_db(args.dsn or default_dsn(), sql)
        )

    if args.list:
        print_users(query(LIST))
        return

    email = literal(args.email.strip())
    if args.level is None:
        rows = query(SHOW.format(email=email))
        if not rows:
            sys.exit(f"no user with e-mail {args.email!r}")
        print_users(rows)
        return

    rows = query(UPDATE.format(email=email, level=literal(args.level)))
    if not rows:
        sys.exit(f"no user with e-mail {args.email!r}")
    for user, old, new in rows:
        print(f"{user}: {old} -> {new}" if old != new else f"{user}: already {new}")


if __name__ == "__main__":
    main()
