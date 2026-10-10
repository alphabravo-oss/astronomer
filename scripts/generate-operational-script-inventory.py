#!/usr/bin/env python3
"""Inventory maintained shell sources; lexical signals are not runtime proofs."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / "docs/assurance/technology-guide/w14/shell-inventory.json"
RELEASE = {"upgrade-release.sh", "release-upgrade-matrix.sh", "rehearse-release-candidate.sh", "run-day2-drill-execution.sh"}
FIXTURE = {"test-live-browser.sh", "test-live-browser-matrix.sh", "smoke-fresh-cluster.sh", "k3d-bootstrap.sh", "k3d-version-matrix-test.sh", "k3d-decommission-matrix-test.sh", "test-worker-runtime-integration.sh", "test-postgres-integration.sh", "test-postgres-failover-certification.sh", "test-postgres-outage-qualification.sh", "test-process-restart-qualification.sh", "test-redis-outage-recovery.sh", "test-tunnel-queue-ha.sh", "test-flux-integration.sh", "migrate-roundtrip-smoke.sh", "query-plan-certification.sh"}
PRIORITY = {"upgrade-release.sh", "test-live-browser.sh", "validate-live-delivery.sh", "smoke-fresh-cluster.sh", "verify-enterprise.sh"}
SIGNALS = {
    "destructive_cleanup": r"\brm\s+[^\n]*-[^\n]*r|\bDROP\s+(?:DATABASE|SCHEMA)|\b(?:delete|dropdb)\b",
    "private_material": r"\b(?:secret|password|token|credential|encryption|private)\b",
    "process_ownership": r"\b(?:trap|kill|wait|port-forward)\b|\$!",
    "time_bounds": r"\b(?:timeout|TIMEOUT|sleep|seq)\b",
    "rollback": r"\b(?:rollback|restore|recovery)\b",
    "evidence": r"\b(?:evidence|artifact|report|SHA256SUMS|jsonfile|junit)\b",
    "checkpoint_or_resume": r"\b(?:checkpoint|resume|replay)\b",
    "ownership_fence": r"\b(?:UID|uid|owner|owned|disposable|context|ack|ACK)\b",
}


def category(path):
    if path.name in RELEASE:
        return "release_state_machine"
    if path.name in FIXTURE:
        return "fixture_generator"
    if path.name.startswith("validate-live-") or path.name in {"astronomer-save-images.sh", "astronomer-load-images.sh", "safe-disk-cleanup.sh", "application-catalog-airgap.sh"}:
        return "runtime_operation"
    if path.parent.name == "lib" or path.name in {"build-gosec.sh", "build-gotestsum.sh", "check-go-lint.sh"}:
        return "small_wrapper"
    return "developer_workflow"


def inventory():
    names = subprocess.check_output(["git", "ls-files", "-z", "scripts"], cwd=ROOT).split(b"\0")
    rows = []
    for name in sorted(x.decode() for x in names if x.endswith(b".sh")):
        path = ROOT / name
        raw = path.read_bytes()
        lines = raw.decode().splitlines()
        rows.append({
            "path": name, "sha256": hashlib.sha256(raw).hexdigest(), "lineCount": len(lines), "category": category(path),
            "priorityCharacterization": path.name in PRIORITY,
            "observedDefaultedEnvironmentNames": sorted(set(re.findall(r"\$\{([A-Z][A-Z0-9_]*)[:-]", raw.decode()))),
            "literalExitCodes": sorted(set(int(x) for x in re.findall(r"\bexit\s+([0-9]+)\b", raw.decode()))),
            "signals": {key: [number for number, line in enumerate(lines, 1) if re.search(pattern, line, re.IGNORECASE)] for key, pattern in SIGNALS.items()},
            "runtimeCharacterization": "pending",
        })
    return {"schemaVersion": 1, "scope": "All Git-tracked scripts/**/*.sh in the bound source checkout", "limitations": ["Category assignments are source inventory, not runtime qualification.", "Environment names and signal line numbers are lexical observations, not an exhaustive shell parser or proof of safety.", "Literal exits omit dynamic child status and trap behavior; priority runner contracts require explicit characterization.", "The historical 65-script plan count is retained in the plan; this inventory binds the actual current source set."], "scriptCount": len(rows), "scripts": rows}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    rendered = json.dumps(inventory(), indent=2) + "\n"
    if args.check:
        if not OUTPUT.is_file() or OUTPUT.read_text() != rendered:
            print("operational shell inventory drift", file=sys.stderr)
            return 1
        print("operational shell source inventory matches")
    else:
        OUTPUT.write_text(rendered)
        print("operational shell source inventory written")
    return 0


if __name__ == "__main__":
    sys.exit(main())
