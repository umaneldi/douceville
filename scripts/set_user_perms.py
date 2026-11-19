#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["psycopg[binary]>=3.1"]
# ///
"""Manage douceville users: list, show, add, remove, and change access levels.

Usage:
    uv run scripts/set_user_perms.py list
    uv run scripts/set_user_perms.py show EMAIL
    uv run scripts/set_user_perms.py set EMAIL LEVEL
    uv run scripts/set_user_perms.py add EMAIL [--level LEVEL] [--first-name F]
                                     [--last-name L] [--password-stdin | --no-password]
    uv run scripts/set_user_perms.py remove EMAIL [--yes]

Every command also accepts [--dsn URL | --psql CMD]. The former syntax
(`--list`, `EMAIL [LEVEL]`) still works.

Levels, in increasing order: unverified < verified < granted < admin. Only
`granted` and `admin` can use the application; a change applies to the next
request of the user (no need to log out). E-mails are matched
case-insensitively.

`add` creates the account directly at LEVEL (default: granted), bypassing the
e-mail verification. The password (8 characters minimum) is prompted for, or
read from the first line of stdin with --password-stdin; with --no-password the
account cannot log in. It is hashed like the Go server does
(PBKDF2-SHA256, 210000 iterations). `remove` deletes the account and its
pending e-mail verifications; it asks for confirmation unless --yes is given.

To work on the docker-compose database without a direct connection, add
    --psql "docker exec -i douceville-geodb-1 psql -U douceville -d douceville"
(use `docker exec -i`, not `-it`: the SQL is piped on stdin).

The DSN defaults to the POSTGRES_* variables (environment, then ./.env), the
same ones the Go server uses.
"""

from __future__ import annotations

import argparse
import base64
import getpass
import hashlib
import os
import re
import shlex
import subprocess
import sys
import uuid
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
INSERT = """
INSERT INTO utilisateur (id, email, first_name, last_name, password_hash, permissions, perms)
VALUES ({id}, {email}, {first_name}, {last_name}, {password_hash}, '{{}}', {level}::user_perms)
ON CONFLICT ((lower(email))) DO NOTHING
RETURNING email, perms::text, first_name, last_name
"""
DELETE = """
DELETE FROM utilisateur WHERE lower(email) = lower({email})
RETURNING email, perms::text, first_name, last_name
"""

MIN_PASSWORD_LEN = 8  # same as the Go server (internal/api/auth_handlers.go)
PBKDF2_ITERATIONS = 210000  # same as internal/auth/password.go
PBKDF2_KEY_LEN = 32
PBKDF2_SALT_LEN = 16
COMMANDS = ("list", "show", "set", "add", "remove")


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


def sql_value(s: str | None) -> str:
    return "NULL" if s is None else literal(s)


def hash_password(password: str) -> str:
    """Same format as auth.HashPassword: pbkdf2_sha256$<iter>$<salt>$<hash> (raw b64)."""
    salt = os.urandom(PBKDF2_SALT_LEN)
    dk = hashlib.pbkdf2_hmac(
        "sha256", password.encode(), salt, PBKDF2_ITERATIONS, PBKDF2_KEY_LEN
    )

    def b64(b: bytes) -> str:
        return base64.b64encode(b).decode().rstrip("=")

    return f"pbkdf2_sha256${PBKDF2_ITERATIONS}${b64(salt)}${b64(dk)}"


def valid_email(email: str) -> bool:
    """Rough equivalent of the server's validEmail: local@domain.tld, no spaces."""
    return len(email) <= 254 and bool(re.fullmatch(r"[^@\s<>]+@[^@\s<>]+\.[^@\s<>]+", email))


def read_password(from_stdin: bool) -> str:
    if from_stdin:
        password = sys.stdin.readline().rstrip("\r\n")
    else:
        password = getpass.getpass("Password: ")
        if getpass.getpass("Confirm password: ") != password:
            sys.exit("passwords do not match")
    if len(password) < MIN_PASSWORD_LEN:
        sys.exit(f"password must be at least {MIN_PASSWORD_LEN} characters")
    return password


def legacy_argv(argv: list[str]) -> list[str]:
    """Translate the former syntax (`--list`, `EMAIL [LEVEL]`) into subcommands."""
    if not argv or argv[0] in COMMANDS or argv[0] in ("-h", "--help"):
        return argv
    if "--list" in argv:
        return ["list"] + [a for a in argv if a != "--list"]
    if "@" in argv[0]:
        if len(argv) > 1 and argv[1] in LEVELS:
            return ["set"] + argv
        return ["show"] + argv
    return argv


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
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument(
        "--dsn", help="PostgreSQL URL (default: built from POSTGRES_* / .env)"
    )
    common.add_argument(
        "--psql",
        metavar="CMD",
        help="run through this psql command instead of connecting with --dsn, e.g. "
        "'docker exec -i douceville-geodb-1 psql -U douceville -d douceville'",
    )
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    sub = parser.add_subparsers(dest="command", required=True, metavar="COMMAND")

    sub.add_parser("list", parents=[common], help="list all users and their level")

    p = sub.add_parser("show", parents=[common], help="show one user")
    p.add_argument("email", help="e-mail of the user")

    p = sub.add_parser("set", parents=[common], help="change the level of a user")
    p.add_argument("email", help="e-mail of the user")
    p.add_argument("level", choices=LEVELS, help="new access level")

    p = sub.add_parser("add", parents=[common], help="create a user")
    p.add_argument("email", help="e-mail of the new user")
    p.add_argument(
        "--level", choices=LEVELS, default="granted", help="access level (default: granted)"
    )
    p.add_argument("--first-name")
    p.add_argument("--last-name")
    pw = p.add_mutually_exclusive_group()
    pw.add_argument(
        "--password-stdin",
        action="store_true",
        help="read the password from the first line of stdin instead of prompting",
    )
    pw.add_argument(
        "--no-password",
        action="store_true",
        help="create the account without a password (it cannot log in)",
    )

    p = sub.add_parser("remove", parents=[common], help="delete a user")
    p.add_argument("email", help="e-mail of the user")
    p.add_argument("-y", "--yes", action="store_true", help="do not ask for confirmation")

    args = parser.parse_args(legacy_argv(sys.argv[1:]))

    def query(sql: str) -> list[tuple]:
        return (
            query_psql(args.psql, sql)
            if args.psql
            else query_db(args.dsn or default_dsn(), sql)
        )

    if args.command == "list":
        print_users(query(LIST))
        return

    raw_email = args.email.strip()
    email = literal(raw_email)

    if args.command == "show":
        rows = query(SHOW.format(email=email))
        if not rows:
            sys.exit(f"no user with e-mail {args.email!r}")
        print_users(rows)

    elif args.command == "set":
        rows = query(UPDATE.format(email=email, level=literal(args.level)))
        if not rows:
            sys.exit(f"no user with e-mail {args.email!r}")
        for user, old, new in rows:
            print(f"{user}: {old} -> {new}" if old != new else f"{user}: already {new}")

    elif args.command == "add":
        new_email = raw_email.lower()  # the server stores e-mails lowercased
        if not valid_email(new_email):
            sys.exit(f"invalid e-mail {args.email!r}")
        password_hash = (
            None if args.no_password else hash_password(read_password(args.password_stdin))
        )
        rows = query(
            INSERT.format(
                id=literal(str(uuid.uuid4())),
                email=literal(new_email),
                first_name=sql_value(args.first_name),
                last_name=sql_value(args.last_name),
                password_hash=sql_value(password_hash),
                level=literal(args.level),
            )
        )
        if not rows:
            sys.exit(f"a user with e-mail {new_email!r} already exists")
        print("created:")
        print_users(rows)

    elif args.command == "remove":
        if not args.yes:
            rows = query(SHOW.format(email=email))
            if not rows:
                sys.exit(f"no user with e-mail {args.email!r}")
            print_users(rows)
            try:
                answer = input("Delete this user? [y/N] ")
            except EOFError:
                answer = ""
            if answer.strip().lower() not in ("y", "yes"):
                sys.exit("aborted")
        rows = query(DELETE.format(email=email))
        if not rows:
            sys.exit(f"no user with e-mail {args.email!r}")
        print("deleted:")
        print_users(rows)


if __name__ == "__main__":
    main()
