#!/usr/bin/env python3
"""Conservative forward-only v2 migration gate for *new* embedded SQL files.

This deliberately accepts only a narrow, reviewable form of ADD COLUMN.
It is not a SQL parser or a proof of version-skew compatibility.
"""
from __future__ import annotations

import argparse
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
MIGRATIONS = pathlib.Path("src/backend/v2/internal/schema/migrations")
IDENT = r"[a-z_][a-z_0-9]*"
TYPE = r"(?:text|integer|bigint|boolean|timestamptz|varchar\([1-9][0-9]{0,3}\))"
ADD = re.compile(
    rf"alter\s+table\s+(?:public\.)?{IDENT}\s+add\s+column\s+"
    rf"(?:if\s+not\s+exists\s+)?{IDENT}\s+{TYPE}(?P<tail>.*)",
    re.IGNORECASE | re.DOTALL,
)
DEFAULT = re.compile(
    r"(?:-?[0-9]+|true|false|null|'(?:[^']|'')*')",
    re.IGNORECASE,
)


def statements(sql: str) -> list[str]:
    """Split SQL, ignoring comments and respecting single-quoted literal semicolons."""
    out: list[str] = []
    buf: list[str] = []
    pos = 0
    state = "normal"
    while pos < len(sql):
        ch = sql[pos]
        nxt = sql[pos + 1] if pos + 1 < len(sql) else ""
        if state == "line":
            if ch == "\n":
                state = "normal"
                buf.append(" ")
        elif state == "block":
            if ch == "*" and nxt == "/":
                state = "normal"
                pos += 1
        elif state == "quote":
            buf.append(ch)
            if ch == "'" and nxt == "'":
                buf.append(nxt)
                pos += 1
            elif ch == "'":
                state = "normal"
        elif ch == "-" and nxt == "-":
            state = "line"
            pos += 1
        elif ch == "/" and nxt == "*":
            state = "block"
            pos += 1
        elif ch == "'":
            state = "quote"
            buf.append(ch)
        elif ch == ";":
            value = "".join(buf).strip()
            if value:
                out.append(value)
            buf = []
        else:
            buf.append(ch)
        pos += 1
    if state in {"quote", "block"}:
        raise ValueError("unterminated SQL string or block comment")
    if "".join(buf).strip():
        raise ValueError("SQL statement missing terminating semicolon")
    return out


def check_sql(sql: str, path: str) -> int:
    try:
        items = statements(sql)
    except ValueError as err:
        print(f"v2-migration-guard: REJECT {path}: {err}", file=sys.stderr)
        return 1
    if not items:
        print(f"v2-migration-guard: REJECT {path}: no SQL statements", file=sys.stderr)
        return 1
    for idx, stmt in enumerate(items, 1):
        normalized = re.sub(r"\s+", " ", stmt.strip())
        match = ADD.fullmatch(normalized)
        if match is None:
            print(
                f"v2-migration-guard: REJECT {path} statement {idx}: "
                "only additive ALTER TABLE ... ADD COLUMN is allowed; "
                "DROP, TYPE changes and unsupported DDL require separate review",
                file=sys.stderr,
            )
            return 1
        tail = match.group("tail").strip()
        if not tail or tail.lower() == "null":
            continue
        # A mandatory addition is allowed only with a simple literal default.
        mandatory = bool(re.search(r"\bnot\s+null\s*$", tail, re.I))
        if mandatory:
            tail = re.sub(r"\bnot\s+null\s*$", "", tail, flags=re.I).strip()
        if not tail.lower().startswith("default "):
            print(
                f"v2-migration-guard: REJECT {path} statement {idx}: "
                "NOT NULL without a safe literal DEFAULT or unsupported clause",
                file=sys.stderr,
            )
            return 1
        literal = tail[8:].strip()
        if not DEFAULT.fullmatch(literal) or (mandatory and literal.lower() == "null"):
            print(
                f"v2-migration-guard: REJECT {path} statement {idx}: "
                "only simple literal DEFAULT is permitted",
                file=sys.stderr,
            )
            return 1
    print(f"v2-migration-guard: PASS {path} statements={len(items)}")
    return 0


def new_migrations(base: str) -> list[pathlib.Path]:
    try:
        subprocess.run(
            ["git", "cat-file", "-e", f"{base}^{{commit}}"],
            cwd=ROOT, check=True, capture_output=True, text=True,
        )
        diff = subprocess.run(
            ["git", "diff", "--name-status", "--no-renames", base, "HEAD",
             "--", str(MIGRATIONS)],
            cwd=ROOT, check=True, capture_output=True, text=True,
        ).stdout
    except subprocess.CalledProcessError as err:
        raise ValueError("invalid baseline commit or git diff failed") from err

    added: list[pathlib.Path] = []
    for line in diff.splitlines():
        status, separator, name = line.partition("\t")
        if not separator:
            raise ValueError("unrecognized migration diff entry")
        path = pathlib.Path(name)
        if status != "A":
            raise ValueError(f"existing embedded migration changed or removed: {name} ({status})")
        if path.suffix != ".sql" or not re.fullmatch(r"[0-9]{3}[_-][a-z0-9_-]+\.sql", path.name):
            raise ValueError(f"invalid new migration filename: {name}")
        if path.parent != MIGRATIONS:
            raise ValueError(f"unexpected migration path: {name}")
        added.append(path)
    return added


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    choices = parser.add_mutually_exclusive_group(required=True)
    choices.add_argument("--file", type=pathlib.Path, help="check one SQL fixture")
    choices.add_argument("--base", help="check new migrations against a Git commit")
    args = parser.parse_args()
    try:
        paths = [args.file] if args.file is not None else new_migrations(args.base)
        for path in paths:
            full = path if path.is_absolute() else ROOT / path
            if not full.is_file():
                raise ValueError(f"missing migration/fixture: {path}")
            if check_sql(full.read_text(encoding="utf-8"), str(path)):
                return 1
    except (ValueError, UnicodeError, OSError) as err:
        print(f"v2-migration-guard: REJECT {err}", file=sys.stderr)
        return 1
    print(f"v2-migration-guard: PASS new_migrations={len(paths)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
