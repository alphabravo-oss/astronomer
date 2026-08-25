#!/usr/bin/env python3
"""Fail closed when a protected enterprise qualification producer is unwired."""

import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PRODUCERS = (
    ("release-candidate-rehearsal", ".github/workflows/release-candidate-rehearsal.yaml", "release-candidate", "workflow_dispatch:"),
    ("cloud-acceptance", ".github/workflows/cloud-acceptance.yaml", "cloud-acceptance", "workflow_dispatch:"),
    ("day2-drill-execution", ".github/workflows/day2-drill-execution.yaml", "scale-certification", "workflow_dispatch:"),
    ("day2-drill-qualification", ".github/workflows/day2-drill-qualification.yaml", "scale-certification", "workflow_dispatch:"),
    ("scale-certification-rungs", ".github/workflows/scale-certification.yaml", "scale-certification", "workflow_dispatch:"),
    ("scale-audit-sizing-aggregate", ".github/workflows/scale-certification-aggregate.yaml", "scale-certification", "workflow_dispatch:"),
    ("rancher-benchmark-automated-human", ".github/workflows/rancher-benchmark.yaml", "rancher-benchmark", "workflow_dispatch:"),
    ("assistive-technology-accessibility", ".github/workflows/accessibility-acceptance.yaml", "accessibility-acceptance", "workflow_dispatch:"),
    ("release-approval-promotion", ".github/workflows/release.yaml", "release-production", 'tags:\n      - "v*.*.*"'),
    ("resumed-release-approval-promotion", ".github/workflows/resume-release.yaml", "release-production", "workflow_dispatch:"),
)


def main() -> None:
    rows = []
    for producer, relative, environment, trigger in PRODUCERS:
        path = ROOT / relative
        if not path.is_file() or path.is_symlink():
            raise SystemExit(f"protected producer {producer} is missing regular workflow {relative}")
        text = path.read_text(encoding="utf-8")
        if trigger not in text or f"environment: {environment}" not in text:
            raise SystemExit(f"protected producer {producer} lacks its exact trigger or environment {environment}")
        rows.append({"id": producer, "workflow": relative, "environment": environment, "trigger": trigger})

    validator = (ROOT / "scripts/validate-release-approval.py").read_text(encoding="utf-8")
    fetcher = (ROOT / "scripts/fetch-release-qualification-artifacts.py").read_text(encoding="utf-8")
    for token in (
        "rc_rehearsal", "cloud_acceptance", "scale_certification",
        "rancher_benchmark", "accessibility",
    ):
        if token not in validator or token not in fetcher:
            raise SystemExit(f"release approval aggregator omits {token}")
    for token in ("validate_rc", "validate_cloud", "validate_scale", "validate_rancher", "validate_accessibility"):
        if token not in validator:
            raise SystemExit(f"release approval aggregator omits {token}")
    static_gate = (ROOT / "scripts/verify-enterprise.sh").read_text(encoding="utf-8")
    pr_aggregate = (ROOT / ".github/workflows/pr-validation.yaml").read_text(encoding="utf-8")
    for producer, *_ in PRODUCERS:
        if f'"{producer}"' not in static_gate:
            raise SystemExit(f"static evidence manifest omits protected producer {producer}")
    if "check-enterprise-qualification-producers.py" not in pr_aggregate:
        raise SystemExit("same-commit PR aggregate does not validate protected producer wiring")
    print(json.dumps({"schema_version": 1, "protected_producers": rows}, sort_keys=True))


if __name__ == "__main__":
    main()
