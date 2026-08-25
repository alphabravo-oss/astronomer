#!/usr/bin/env python3
"""Download the exact external qualification artifacts named by an approval."""

import argparse
import json
import re
import subprocess
from pathlib import Path

QUALIFICATIONS = {
    "rc_rehearsal": (".github/workflows/release-candidate-rehearsal.yaml", ("rc-rehearsal-evidence.json", "rc-rehearsal-evidence.sigstore.json")),
    "cloud_acceptance": (".github/workflows/cloud-acceptance.yaml", ("cloud-acceptance.json", "cloud-acceptance.json.sigstore.json")),
    "scale_certification": (".github/workflows/scale-certification-aggregate.yaml", ("certification-set.json", "certification-set.sigstore.json")),
    "rancher_benchmark": (".github/workflows/rancher-benchmark.yaml", ("benchmark-v1.json", "benchmark-v1.sigstore.json")),
    "accessibility": (".github/workflows/accessibility-acceptance.yaml", ("accessibility-evidence.json", "accessibility-evidence.sigstore.json")),
}
RUN_ID = re.compile(r"[1-9][0-9]*\Z")
ARTIFACT = re.compile(r"[a-z0-9][a-z0-9._-]{2,127}\Z")


def validate_run_metadata(run: dict, artifacts: dict, *, repository: str, workflow: str, commit: str, tag: str, artifact: str) -> None:
    if not isinstance(run, dict) or run.get("status") != "completed" or run.get("conclusion") != "success" or run.get("event") != "workflow_dispatch":
        raise ValueError("qualification source run is not a completed successful dispatch")
    if run.get("path") != workflow or run.get("head_sha") != commit or run.get("head_branch") != tag or run.get("head_repository", {}).get("full_name") != repository:
        raise ValueError("qualification source run workflow/ref/commit/repository identity mismatch")
    rows = artifacts.get("artifacts") if isinstance(artifacts, dict) else None
    matches = [row for row in rows or [] if isinstance(row, dict) and row.get("name") == artifact and row.get("expired") is False]
    if len(matches) != 1:
        raise ValueError("qualification source must expose one unique non-expired artifact")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--approval", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--tag", required=True)
    args = parser.parse_args()
    approval = json.loads(args.approval.read_text(encoding="utf-8"))
    if args.out.exists():
        raise SystemExit("qualification output directory must not already exist")
    args.out.mkdir(parents=True, mode=0o700)
    for name, (workflow, required_files) in QUALIFICATIONS.items():
        descriptor = approval.get(name)
        if not isinstance(descriptor, dict):
            raise SystemExit(f"approval is missing {name}")
        run_id, artifact = descriptor.get("source_run_id", ""), descriptor.get("artifact_name", "")
        if not RUN_ID.fullmatch(run_id) or not ARTIFACT.fullmatch(artifact):
            raise SystemExit(f"approval {name} download identity is invalid")
        run = json.loads(subprocess.run(["gh", "api", f"repos/{args.repository}/actions/runs/{run_id}"], check=True, capture_output=True, text=True).stdout)
        artifacts = json.loads(subprocess.run(["gh", "api", f"repos/{args.repository}/actions/runs/{run_id}/artifacts?per_page=100&name={artifact}"], check=True, capture_output=True, text=True).stdout)
        validate_run_metadata(run, artifacts, repository=args.repository, workflow=workflow, commit=args.source_commit, tag=args.tag, artifact=artifact)
        target = args.out / name
        subprocess.run(
            ["gh", "run", "download", run_id, "--repo", args.repository, "--name", artifact, "--dir", str(target)],
            check=True,
        )
        for filename in required_files:
            path = target / filename
            if not path.is_file() or path.is_symlink():
                raise SystemExit(f"{name} artifact is missing required regular file {filename}")


if __name__ == "__main__":
    main()
