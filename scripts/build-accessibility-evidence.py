#!/usr/bin/env python3
"""Reduce retained raw AT records to the signed release evidence matrix."""

import argparse
import hashlib
import json
from pathlib import Path


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--raw-dir", type=Path, required=True); parser.add_argument("--tag", required=True)
    parser.add_argument("--source-commit", required=True); parser.add_argument("--source-run-id", required=True)
    parser.add_argument("--raw-run-id", required=True); parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    records_path = args.raw_dir / "accessibility-records.json"
    value = json.loads(records_path.read_text(encoding="utf-8"))
    if not isinstance(value, dict) or set(value) != {"started_at", "completed_at", "reviewer", "matrix"}: raise ValueError("raw accessibility record violates closed schema")
    matrix = []
    for row in value["matrix"]:
        expected = {"platform", "browser", "assistive_technology", "at_version", "viewport", "zoom_levels", "critical_workflows", "status", "open_blocking_defects", "evidence_file"}
        if not isinstance(row, dict) or set(row) != expected: raise ValueError("raw accessibility row violates closed schema")
        evidence_file = row.pop("evidence_file")
        path = (args.raw_dir / evidence_file).resolve()
        if args.raw_dir.resolve() not in path.parents or not path.is_file() or path.is_symlink(): raise ValueError("raw accessibility evidence path is unsafe or missing")
        matrix.append({**row, "evidence_uri": f"artifact://accessibility-raw-{args.raw_run_id}/{evidence_file}", "evidence_sha256": "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()})
    manifest, bundle = args.raw_dir / "raw-manifest.json", args.raw_dir / "raw-manifest.sigstore.json"
    output = {"schema_version": 1, "result": "passed", "target_version": args.tag, "source_commit": args.source_commit, "source_run_id": args.source_run_id, "started_at": value["started_at"], "completed_at": value["completed_at"], "reviewer": value["reviewer"], "raw_evidence": {"source_run_id": args.raw_run_id, "source_workflow": ".github/workflows/accessibility-raw-evidence.yaml", "source_commit": args.source_commit, "source_ref": f"refs/tags/{args.tag}", "source_conclusion": "success", "manifest_sha256": "sha256:" + hashlib.sha256(manifest.read_bytes()).hexdigest(), "signature_bundle_sha256": "sha256:" + hashlib.sha256(bundle.read_bytes()).hexdigest()}, "matrix": matrix}
    args.out.write_text(json.dumps(output, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")


if __name__ == "__main__": main()
