#!/usr/bin/env python3
"""Require explicit owners and reader behavior for discovered JSON contracts.

Go AST discovery binds encoding/json import aliases and canonical sqlc model
columns. This is a bounded syntax inventory, not whole-program dataflow proof;
runtime SQL triggers and behavior tests remain required.
"""
from __future__ import annotations

import json
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
POLICY = ROOT / "docs/data-governance/json-contracts.json"
CLASSES = {"strict_product", "versioned_internal", "raw_kubernetes", "extension_data", "opaque_external", "non_json"}


def validate(actual: dict, policy: dict) -> tuple[int, int]:
    if type(policy.get("schema_version")) is not int or policy["schema_version"] != 1:
        raise ValueError("JSON contract inventory requires schema_version=1")
    declared = {}
    for row in policy["columns"]:
        key = (row["model"], row["column"])
        if key in declared:
            raise ValueError(f"duplicate JSON column contract: {key}")
        if row["classification"] not in CLASSES or (type(row["schema_version"]) is not int or row["schema_version"]<1):
            raise ValueError(f"invalid JSON column classification/version: {key}")
        for field in ("owner", "schema", "reader_behavior"):
            if not isinstance(row.get(field), str) or not row[field].strip():
                raise ValueError(f"JSON column {key} lacks {field}")
        if row["classification"] == "non_json" and (row.get("storage") not in {"byte_slot", "encrypted_slot"} or not row.get("exclusion_reason")):
            raise ValueError(f"JSON exclusion requires exact storage and reason: {key}")
        if row["classification"] == "non_json" and row.get("sql_type") in {"json", "jsonb"}:
            raise ValueError(f"physical JSON column cannot be excluded: {key}")
        declared[key] = row
    discovered = {(row["model"], row["column"]) for row in actual["columns"]}
    if discovered != set(declared):
        raise ValueError(f"JSON persistence drift: unowned={sorted(discovered-set(declared))}; stale={sorted(set(declared)-discovered)}")
    for row in actual["columns"]:
        if declared[(row["model"], row["column"])].get("storage") != row.get("storage"):
            raise ValueError(f"JSON storage representation drift: {row['model']}.{row['column']}")

    modules = {}
    for row in policy.get("json_modules", []):
        if row["path"] in modules or not row.get("owner") or not row.get("scope"):
            raise ValueError("serializer module lacks explicit ownership/scope")
        modules[row["path"]] = row
    paths = {row["path"] for row in actual["json_calls"]}
    if paths != set(modules):
        raise ValueError(f"serializer ownership drift: unowned={sorted(paths-set(modules))}; stale={sorted(set(modules)-paths)}")

    requests = {}
    for row in policy["requests"]:
        key = row["key"]
        if key in requests or row["classification"] not in CLASSES - {"non_json"}:
            raise ValueError(f"duplicate/unclassified request decoder: {key}")
        if row["unknown_fields"] not in {"exact_product", "additive_v1"} or not row["owner"] or row["malformed_body"] not in {"typed_error_no_write", "deny_elevation_preserve_read_only_session"}:
            raise ValueError(f"missing request compatibility/error owner: {key}")
        if row["malformed_body"] != "typed_error_no_write" and key != "internal/handler/kubectl_shell_sessions.go:parseShellElevation:decodeV1JSONBody":
            raise ValueError(f"non-mutating compatibility exception is not leased: {key}")
        requests[key] = row
    found = {row["path"]+":"+row["function"]+":"+row["operation"] for row in actual["requests"] if row["path"].startswith("internal/handler/")}
    if found != set(requests):
        raise ValueError(f"request decoder drift: unowned={sorted(found-set(requests))}; stale={sorted(set(requests)-found)}")

    def exception_key(row: dict) -> tuple:
        return (row["path"], row["function"], row["operation"], tuple(row["arguments"]))

    exceptions = {exception_key(row) for row in policy["nonrequest_decoder_exceptions"]}
    raw = {exception_key(row) for row in actual["json_calls"] if row["path"].startswith("internal/handler/") and row["operation"].endswith(".NewDecoder")}
    if raw != exceptions:
        raise ValueError(f"noncanonical request/JSON decoder: unowned={sorted(raw-exceptions)}; stale={sorted(exceptions-raw)}")

    sites = {}
    for row in policy.get("json_sites", []):
        key = exception_key(row)
        if key in sites or not all(isinstance(row.get(field), str) and row[field].strip() for field in ("owner", "schema", "reader_behavior")):
            raise ValueError(f"JSON site lacks unique ownership/schema/reader behavior: {key}")
        sites[key] = row
    observed = {exception_key(row) for row in actual["json_calls"]}
    if observed != set(sites):
        raise ValueError(f"JSON site drift: unowned={sorted(observed-set(sites))}; stale={sorted(set(sites)-observed)}")
    return len(found), len(discovered)


def main() -> None:
    actual = json.loads(subprocess.check_output(["go", "run", "./tools/json-governance"], cwd=ROOT, text=True))
    policy = json.loads(POLICY.read_text())
    requests, columns = validate(actual, policy)
    for row in policy["columns"]:
        for binding in row.get("canonical_helper_bindings", []):
            path = (ROOT / binding["path"]).resolve()
            if not path.is_relative_to(ROOT) or not path.is_file():
                raise ValueError("canonical helper binding does not name an owned source file")
            functions = re.findall(r"\bfunc\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(", path.read_text())
            if binding["function"] not in functions:
                raise ValueError(f"canonical helper function no longer exists: {binding['path']}:{binding['function']}")
    print(f"json-governance: {requests} request owners; {columns} storage candidates classified (including exact binary/scalar exclusions); {len(actual['json_calls'])} serializer/read sites discovered")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, subprocess.CalledProcessError) as exc:
        print(f"json-governance: {exc}", file=sys.stderr)
        sys.exit(1)
