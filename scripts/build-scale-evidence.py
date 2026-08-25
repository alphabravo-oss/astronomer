#!/usr/bin/env python3
"""Build a deterministic, digest-bound scale-certification evidence manifest."""

import argparse
import hashlib
import json
from pathlib import Path

SCHEMA = "astronomer-scale-evidence-v1"
REPORT_SCHEMA = "astronomer-scale-report-v2"


def digest(path: Path) -> dict:
    body = path.read_bytes()
    return {"path": path.as_posix(), "sha256": hashlib.sha256(body).hexdigest(), "size": len(body)}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--evidence-dir", type=Path, required=True)
    parser.add_argument("--profile", required=True)
    args = parser.parse_args()
    root = args.evidence_dir.resolve()
    report_path = root / f"{args.profile}.md.json"
    report = json.loads(report_path.read_text(encoding="utf-8"))
    if report.get("schema_version") != REPORT_SCHEMA or report.get("profile") != args.profile:
        raise SystemExit("report schema/profile does not match the requested evidence set")
    metadata = report.get("metadata", {})
    traffic = report.get("traffic", {})
    audit = report.get("mandatory_audit", {})
    baseline = {
        "schema_version": "astronomer-scale-baseline-row-v1",
        "profile": args.profile,
        "commit": metadata.get("commit"),
        "images": metadata.get("images"),
        "verdict": report.get("verdict"),
        "target_rps": report.get("target_rps"),
        "observed_rps": traffic.get("observed_rps"),
        "requests": traffic.get("requests"),
        "duration_seconds": traffic.get("observed_duration_seconds"),
        "resource_cardinality": report.get("resource_cardinality"),
        "state_events_emitted": report.get("state_events_emitted"),
        "audit_accepted": audit.get("accepted"),
        "audit_canonical_rows": audit.get("canonical_rows"),
        "audit_duplicates": audit.get("duplicates"),
        "audit_lost": audit.get("lost"),
    }
    baseline_path = root / "baseline-row.json"
    baseline_path.write_text(
        json.dumps(baseline, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8"
    )
    required = [
        root / f"{args.profile}.md",
        report_path,
        root / f"{args.profile}.md.sha256",
        root / "rendered-values.yaml",
        baseline_path,
    ]
    drill_files = sorted((root / "drills").glob("*.json"))
    raw_files = sorted(path for path in (root / "raw").rglob("*") if path.is_file())
    if not drill_files or not raw_files:
        raise SystemExit("evidence requires retained drill JSON and raw qualification data")
    for path in required:
        if not path.is_file():
            raise SystemExit(f"required evidence file is missing: {path}")
    files = sorted(required + drill_files + raw_files)
    entries = []
    for path in files:
        relative = path.relative_to(root)
        item = digest(path)
        item["path"] = relative.as_posix()
        entries.append(item)
    manifest = {
        "schema_version": SCHEMA,
        "generated_at": report.get("generated_at"),
        "profile": args.profile,
        "verdict": report.get("verdict"),
        "release": {
            "commit": metadata.get("commit"),
            "images": metadata.get("images"),
            "chart_values": metadata.get("chart_values"),
            "environment": metadata.get("environment"),
            "kubernetes_version": metadata.get("kubernetes_version"),
            "postgres_version": metadata.get("postgres_version"),
            "redis_version": metadata.get("redis_version"),
            "hardware": metadata.get("hardware"),
        },
        "provenance": {
            "run_id": metadata.get("run_id"),
            "drill_evidence_run_id": metadata.get("drill_evidence_run_id"),
        },
        "files": entries,
    }
    (root / "evidence-manifest.json").write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )


if __name__ == "__main__":
    main()
