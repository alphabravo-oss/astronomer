#!/usr/bin/env python3
"""Bounded syntax ownership gate; it does not prove a whole SQL call graph."""
from __future__ import annotations

import collections
import hashlib
import json
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
POLICY = ROOT / "docs/data-governance/runtime-sql.json"
MAX_LEGACY_SITES = 294
CLASSES = {"fixed-sql", "advisory-lock", "extension-template", "dynamic-audit-filter", "dynamic-audit-batch", "dynamic-audit-partition", "fixed-selection", "low-level-runtime", "non-sql-candidate"}
BUILDERS = {
    "audit-filter": {"internal/db/queryextensions/audit_v1_filter_manual.go.tmpl", "internal/db/queryextensions/audit_v1_manual.go.tmpl"},
    "audit-batch": {"internal/db/queryextensions/audit_v1_manual.go.tmpl"},
    "audit-partition": {"internal/db/queryextensions/audit_v1_manual.go.tmpl", "internal/worker/tasks/audit_retention.go"},
    "charlie-fixed-selection": {"internal/charlie/admin_visibility_capability_adapter.go", "internal/charlie/runtime_capability_adapter.go", "internal/charlie/mode_store.go"},
}
STOPPED = {"PruneOldImageVulnerabilityReportSnapshots", "DeleteClusterServiceMesh"}
SITE_FIELDS = {"path", "function", "operation", "sha256", "scope_sha256", "classification", "control"}


def inventory(root: pathlib.Path = ROOT) -> dict:
    result = subprocess.run(["go", "run", str(ROOT / "tools/sql-governance"), str(root)], cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)
    return json.loads(result.stdout)


def identity(site: dict) -> tuple:
    return tuple(site[key] for key in ("path", "function", "operation", "sha256", "scope_sha256"))


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def valid_hash(value: object) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[0-9a-f]{64}", value) is not None


def verify(policy: dict, actual: dict, root: pathlib.Path = ROOT) -> set[str]:
    require(set(policy) == {"schema_version", "legacy_site_ceiling", "owners", "sites", "builders", "stopped_methods"}, "runtime SQL policy fields are not closed")
    require(type(policy["schema_version"]) is int and policy["schema_version"] == 1, "runtime SQL schema_version must be integer 1")
    require(type(policy["legacy_site_ceiling"]) is int and 0 <= policy["legacy_site_ceiling"] <= MAX_LEGACY_SITES, "legacy site ceiling must be an integer within the reviewed no-growth bound")
    require(type(actual["schema_version"]) is int and actual["schema_version"] == 1, "discovery version differs")
    require(isinstance(policy["owners"], list) and isinstance(policy["sites"], list), "owners/sites must be lists")
    owners = {}
    for owner in policy["owners"]:
        require(set(owner) == {"path", "owner", "source_sha256"}, "SQL owner fields differ")
        path = owner["path"]
        require(isinstance(path, str) and path not in owners and not pathlib.PurePosixPath(path).is_absolute() and ".." not in pathlib.PurePosixPath(path).parts, "SQL owner path invalid or duplicated")
        require(isinstance(owner["owner"], str) and owner["owner"].strip() and valid_hash(owner["source_sha256"]), "SQL owner or source binding absent")
        source = root / path
        require(source.is_file() and hashlib.sha256(source.read_bytes()).hexdigest() == owner["source_sha256"], f"SQL owner source changed: {path}")
        owners[path] = owner
    legacy_count = 0
    for site in policy["sites"]:
        require(set(site) == SITE_FIELDS, "SQL site fields differ")
        require(site["path"] in owners, "SQL site has no owner")
        require(site["classification"] in CLASSES and isinstance(site["control"], str) and site["control"].strip(), "SQL site classification/control absent")
        require(valid_hash(site["sha256"]) and valid_hash(site["scope_sha256"]), "SQL call/declaration binding absent")
        require(all(isinstance(site[key], str) and site[key] for key in ("path", "function", "operation")), "SQL site identity absent")
        legacy_count += site["classification"] != "non-sql-candidate"
    require(legacy_count <= policy["legacy_site_ceiling"], "legacy raw SQL allowance grew; use canonical queries or an explicit bounded replacement")
    require(set(owners) == {site["path"] for site in actual["sites"]} | set().union(*BUILDERS.values()), "SQL owner module inventory drift")
    expected = collections.Counter(identity(site) for site in policy["sites"])
    found = collections.Counter(identity(site) for site in actual["sites"])
    require(expected == found, "runtime SQL call/literal/declaration drift; new, removed or changed sites require exact ownership review")
    require(isinstance(policy["builders"], list), "builder registry must be a list")
    seen_builders = set()
    for builder in policy["builders"]:
        require(set(builder) == {"name", "paths", "mechanism", "verification"}, "builder fields differ")
        name = builder["name"]
        require(name in BUILDERS and name not in seen_builders and set(builder["paths"]) == BUILDERS[name], "builder name/closure differs from finite allowlist")
        require(all(path in owners for path in builder["paths"]), "builder helper/constant source has no exact binding")
        require(all(isinstance(builder[key], str) and builder[key].strip() for key in ("mechanism", "verification")), "builder safety evidence absent")
        seen_builders.add(name)
    require(seen_builders == set(BUILDERS), "builder closure missing")
    require(isinstance(policy["stopped_methods"], list), "stopped methods must be a list")
    stopped = set()
    for method in policy["stopped_methods"]:
        require(set(method) == {"method", "status", "activation_requirement"}, "stopped method fields differ")
        require(method["method"] in STOPPED and method["method"] not in stopped and method["status"] == "dormant-policy-unresolved", "dormant policy status must remain explicit")
        require(isinstance(method["activation_requirement"], str) and method["activation_requirement"].strip(), "dormant activation requirements absent")
        stopped.add(method["method"])
    require(stopped == STOPPED, "dormant method inventory differs")
    require(not any(site["dynamic_delete"] for site in actual["sites"]), "unresolved dynamic DELETE construction; no approved runtime builder exists")
    require(not any(site["operation"] == "StoppedActivation" for site in actual["sites"]), "dormant delete activation found; policy/authority/retention review is required before a runtime reference")
    # No raw query candidate may return to the four converted domain services.
    selected = {"systemrollout", "systemrelease", "catalogapp", "builtin"}
    for site in actual["sites"]:
        parts = pathlib.PurePosixPath(site["path"]).parts
        if len(parts) > 2 and parts[:2] == ("internal", "delivery") and parts[2] in selected:
            require(site["operation"] == "SQLLiteral" and next(p for p in policy["sites"] if identity(p) == identity(site))["classification"] == "non-sql-candidate", "converted service reintroduced raw SQL")
    return {table for site in actual["sites"] for table in site["delete_targets"]}


def check_generated_namespace(root: pathlib.Path = ROOT) -> None:
    # Output ownership comes from canonical source filenames, not a forgeable
    # generated comment or arbitrary filename suffix. Freshness remains sqlc-check.
    directory = root / "internal/db/sqlc"
    allowed = {path.stem + ".sql.go" for path in (root / "internal/db/queries").glob("*.sql")}
    allowed |= {"db.go", "models.go", "querier.go"}
    allowed |= {path.name.removesuffix(".tmpl") for path in (root / "internal/db/queryextensions").glob("*.go.tmpl")}
    actual = {path.name for path in directory.glob("*.go") if not path.name.endswith("_test.go")}
    require(actual <= allowed, "unowned SQL generated-namespace file; a generated header cannot authorize runtime SQL")


def main() -> None:
    try:
        check_generated_namespace()
        actual = inventory()
        policy = json.loads(POLICY.read_text())
        targets = verify(policy, actual)
        print(f"sql-governance: {len(actual['sites'])} exact candidates; {len(policy['owners'])} source-bound owners; {len(targets)} embedded delete targets; two dormant activations blocked")
    except (ValueError, KeyError, TypeError, OSError, subprocess.CalledProcessError) as error:
        print(f"sql-governance: {error}", file=sys.stderr)
        raise SystemExit(1) from error


if __name__ == "__main__":
    main()
