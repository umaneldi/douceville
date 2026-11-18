#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["psycopg[binary]>=3.1"]
# ///
"""Create (or update) a PostgreSQL role with read-only access to the database.

Usage:
    uv run scripts/create_readonly_user.py ROLE [--password PW] [--schema SCHEMA ...] \
        [--dsn URL | --psql CMD]

The role gets LOGIN, CONNECT on the current database, USAGE on the schemas
(default: public) and SELECT on all their tables and sequences. Default
privileges are also set so that tables later created *by the connecting role*
are readable too. If the role already exists, its password is reset and the
grants are applied again (the script is idempotent).

Without --password, a random password is generated and printed.

To work on the docker-compose database without a direct connection, add
    --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the SQL is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses. The connecting role needs CREATEROLE (or be
superuser) and must own the tables, or have grant rights on them.
"""

from __future__ import annotations

import argparse
import os
import secrets
import shlex
import subprocess
import sys
from pathlib import Path


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


def ident(s: str) -> str:
    """SQL quoted identifier."""
    return '"' + s.replace('"', '""') + '"'


def statements(role: str, password: str, schemas: list[str]) -> list[str]:
    r, pw = ident(role), literal(password)
    # The role name and password are embedded as literals in format() calls so
    # that the DO block needs no dollar-quoted user data.
    sql = [
        f"""DO $do$
BEGIN
  IF EXISTS (SELECT FROM pg_roles WHERE rolname = {literal(role)}) THEN
    EXECUTE format('ALTER ROLE %I WITH LOGIN PASSWORD %L', {literal(role)}, {pw});
  ELSE
    EXECUTE format('CREATE ROLE %I WITH LOGIN PASSWORD %L', {literal(role)}, {pw});
  END IF;
  EXECUTE format('GRANT CONNECT ON DATABASE %I TO %I', current_database(), {literal(role)});
END
$do$""",
    ]
    for schema in schemas:
        s = ident(schema)
        sql += [
            f"GRANT USAGE ON SCHEMA {s} TO {r}",
            f"GRANT SELECT ON ALL TABLES IN SCHEMA {s} TO {r}",
            f"GRANT SELECT ON ALL SEQUENCES IN SCHEMA {s} TO {r}",
            f"ALTER DEFAULT PRIVILEGES IN SCHEMA {s} GRANT SELECT ON TABLES TO {r}",
            f"ALTER DEFAULT PRIVILEGES IN SCHEMA {s} GRANT SELECT ON SEQUENCES TO {r}",
        ]
    return sql


def run_psql(cmd: str, sql: list[str]) -> None:
    """Run the statements in one transaction through the psql command `cmd`."""
    script = (
        "\\set ON_ERROR_STOP on\n\\set QUIET on\nBEGIN;\n"
        + "".join(s + ";\n" for s in sql)
        + "COMMIT;\n"
    )
    try:
        proc = subprocess.run(shlex.split(cmd), input=script, text=True)
    except OSError as e:
        sys.exit(f"--psql: cannot run {cmd!r}: {e}")
    if proc.returncode != 0:
        sys.exit(f"--psql: query failed (exit code {proc.returncode})")


def run_db(dsn: str, sql: list[str]) -> None:
    import psycopg

    with psycopg.connect(dsn) as conn, conn.cursor() as cur:  # commits on success
        for s in sql:
            cur.execute(s)


def main() -> None:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("role", help="name of the read-only role")
    parser.add_argument(
        "--password", help="password of the role (default: random, printed)"
    )
    parser.add_argument(
        "--schema",
        action="append",
        dest="schemas",
        metavar="SCHEMA",
        help="schema to grant read access to (repeatable, default: public)",
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

    password = args.password or secrets.token_urlsafe(24)
    sql = statements(args.role, password, args.schemas or ["public"])
    if args.psql:
        run_psql(args.psql, sql)
    else:
        run_db(args.dsn or default_dsn(), sql)

    print(f"read-only role {args.role!r} ready")
    if not args.password:
        print(f"password: {password}")


if __name__ == "__main__":
    main()
