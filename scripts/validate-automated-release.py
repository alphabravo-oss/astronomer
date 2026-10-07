#!/usr/bin/env python3
"""Verify the explicitly scoped v1.2.0 automated publication qualification."""

import argparse
import importlib.util
import json
from pathlib import Path

spec = importlib.util.spec_from_file_location("approval", Path(__file__).with_name("validate-release-approval.py"))
approval = importlib.util.module_from_spec(spec)
spec.loader.exec_module(approval)


def validate(args, directory=Path(".")):
    if args.tag != "v1.2.0":
        raise ValueError("automated publication exception applies only to v1.2.0")
    if not approval.COMMIT.fullmatch(args.source_commit) or any(
        not approval.RUN_ID.fullmatch(run) for run in (args.source_run_id, args.producer_run_id)
    ):
        raise ValueError("invalid commit or workflow run identity")
    release_identity = f"https://github.com/{args.repository}/.github/workflows/release.yaml@refs/tags/{args.tag}"
    allowed_rc = {
        release_identity,
        f"https://github.com/{args.repository}/.github/workflows/resume-release.yaml@refs/heads/main",
        f"https://github.com/{args.repository}/.github/workflows/resume-release.yaml@refs/tags/{args.tag}",
    }
    if args.rc_identity not in allowed_rc:
        raise ValueError("unexpected rehearsal producer workflow")
    values = {}
    for name, identity in (("release-manifest", release_identity), ("runtime-image-evidence", release_identity), ("rc-rehearsal-evidence", args.rc_identity)):
        path = directory / f"{name}.json"
        values[name] = approval.read_json(path, name)
        approval.verify_signature(path, directory / f"{name}.sigstore.json", identity)
    manifest = values["release-manifest"]
    if manifest.get("release", {}).get("version") != args.tag or manifest["release"].get("source_commit") != args.source_commit:
        raise ValueError("manifest does not bind the exact release")
    digest = approval.sha256(directory / "release-manifest.json")
    runtime = values["runtime-image-evidence"]
    if runtime.get("result") != "passed" or runtime.get("release_version") != args.tag or runtime.get("release_manifest_sha256") != digest:
        raise ValueError("runtime image qualification does not bind the manifest")
    rc = values["rc-rehearsal-evidence"]
    approval.validate_rc(rc, args.tag, args.source_commit, args.source_run_id, args.producer_run_id)
    if rc["previous_version"] != "v1.1.0" or rc["release_manifest_sha256"] != digest:
        raise ValueError("rehearsal does not qualify v1.1.0 to exact v1.2.0 artifacts")
    if approval.stamp(rc["completed_at"], "RC completion") < approval.stamp(rc["started_at"], "RC start"):
        raise ValueError("invalid rehearsal timestamps")
    return {
        "schema_version": 1, "policy": "v1.2.0-automated-publication",
        "tag": args.tag, "source_commit": args.source_commit,
        "source_run_id": args.source_run_id, "producer_run_id": args.producer_run_id,
        "release_manifest_sha256": digest,
        "runtime_image_evidence_sha256": approval.sha256(directory / "runtime-image-evidence.json"),
        "rc_rehearsal_evidence_sha256": approval.sha256(directory / "rc-rehearsal-evidence.json"),
        "external_certifications": {name: "deferred" for name in (
            "cloud_acceptance", "scale_certification", "rancher_benchmark", "human_accessibility")},
        "named_release_approver": "not_required_by_v1.2.0_policy",
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("tag", "source-commit", "source-run-id", "producer-run-id", "repository", "rc-identity"):
        parser.add_argument(f"--{name}", required=True)
    result = validate(parser.parse_args())
    Path("release-qualification.json").write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    main()
