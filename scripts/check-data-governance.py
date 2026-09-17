#!/usr/bin/env python3
"""Verify the closed destructive-delete inventory and JSON governance migration."""

from __future__ import annotations

import json
import pathlib
import re
import sys


ROOT = pathlib.Path(__file__).resolve().parents[1]
POLICY_PATH = ROOT / "docs" / "data-governance" / "deletion-policies.json"
QUERY_DIR = ROOT / "internal" / "db" / "queries"
MIGRATION_PATH = ROOT / "internal" / "db" / "migrations" / "049_durable_json_governance.up.sql"

DELETE_RE = re.compile(
    r"\bDELETE\s+FROM\s+(?:public\.)?([a-z_][a-z0-9_]*)", re.IGNORECASE
)


def fail(message: str) -> None:
    print(f"data-governance: {message}", file=sys.stderr)
    raise SystemExit(1)


def delete_targets() -> set[str]:
    targets: set[str] = set()
    for path in sorted(QUERY_DIR.glob("*.sql")):
        source = re.sub(r"--[^\n]*", "", path.read_text(encoding="utf-8"))
        targets.update(match.group(1).lower() for match in DELETE_RE.finditer(source))
    return targets


def declared_policies() -> set[str]:
    document = json.loads(POLICY_PATH.read_text(encoding="utf-8"))
    if set(document) != {"schema_version", "policies"} or document["schema_version"] != 1:
        fail("deletion policy must be the closed schema_version=1 document")

    declared: set[str] = set()
    required = {"classification", "owner", "mechanism", "audit", "tables"}
    for index, policy in enumerate(document["policies"]):
        if set(policy) != required:
            fail(f"policy {index} fields differ from {sorted(required)}")
        if not all(isinstance(policy[key], str) and policy[key].strip() for key in required - {"tables"}):
            fail(f"policy {index} has an empty ownership field")
        tables = policy["tables"]
        if not isinstance(tables, list) or not tables or tables != sorted(tables):
            fail(f"policy {index} tables must be a non-empty sorted list")
        for table in tables:
            if not isinstance(table, str) or not re.fullmatch(r"[a-z_][a-z0-9_]*", table):
                fail(f"policy {index} has invalid table {table!r}")
            if table in declared:
                fail(f"table {table} is classified more than once")
            declared.add(table)
    return declared


def check_json_governance_migration() -> None:
    source = MIGRATION_PATH.read_text(encoding="utf-8")
    required_fragments = (
        "CREATE TABLE public.durable_json_schemas",
        "schema_version",
        "max_bytes",
        "CREATE FUNCTION public.validate_durable_jsonb_write()",
        "CREATE TRIGGER durable_json_validate_write",
        "CREATE VIEW public.durable_json_schema_coverage",
    )
    missing = [fragment for fragment in required_fragments if fragment not in source]
    if missing:
        fail(f"migration 049 is missing JSON governance contracts: {missing}")


def main() -> None:
    actual = delete_targets()
    declared = declared_policies()
    if actual != declared:
        missing = sorted(actual - declared)
        stale = sorted(declared - actual)
        fail(f"delete inventory drift; unclassified={missing}, stale={stale}")
    check_json_governance_migration()
    print(
        f"data-governance: {len(actual)} destructive tables classified; "
        "durable JSON writer governance present"
    )


if __name__ == "__main__":
    main()
