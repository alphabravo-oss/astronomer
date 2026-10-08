#!/usr/bin/env python3
"""Verify release metadata, including the bounded v1.2.0 packaging recovery."""
import argparse
import hashlib
import json
import re
import subprocess
from pathlib import Path

REPOSITORY = "alphabravo-oss/astronomer"
RECOVERY_TAG = "v1.2.0"
RECOVERY_COMMIT = "4a9986c22a582d71273da61e398b03f0dde4e8d8"
RECOVERY_SOURCE_RUN = "37705872945"
RECOVERY_IDENTITY = f"https://github.com/{REPOSITORY}/.github/workflows/resume-release.yaml@refs/heads/main"
ISSUER = "https://token.actions.githubusercontent.com"
DIGEST = re.compile(r"sha256:[a-f0-9]{64}")
FILES = {"release-manifest.json", "runtime-image-evidence.json", "runtime-image-evidence.tar.gz", "astronomer-1.2.0.tgz"}


def sha(path):
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def verify_signature(path, bundle, identity):
    subprocess.run(["cosign", "verify-blob", "--bundle", str(bundle),
                    "--certificate-identity", identity, "--certificate-oidc-issuer", ISSUER,
                    str(path)], check=True, stdout=subprocess.DEVNULL)


def metadata_identity(directory, tag, source_commit=None, source_run_id=None, manifest_path=None):
    manifest_path = manifest_path or directory / "release-manifest.json"
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", tag):
        raise ValueError("invalid release version")
    proof = directory / "release-recovery.json"
    bundle = directory / "release-recovery.sigstore.json"
    if not proof.exists() and not bundle.exists():
        return f"https://github.com/{REPOSITORY}/.github/workflows/release.yaml@refs/tags/{tag}"
    if tag != RECOVERY_TAG or not proof.is_file() or not bundle.is_file():
        raise ValueError("incomplete or unsupported release recovery")
    verify_signature(proof, bundle, RECOVERY_IDENTITY)
    record = json.loads(proof.read_text())
    keys = {"schema_version", "tag", "source_commit", "source_run_id", "producer_run_id", "producer_commit", "files"}
    if not isinstance(record, dict) or set(record) != keys:
        raise ValueError("invalid recovery record schema")
    if (record["schema_version"] != 1 or record["tag"] != RECOVERY_TAG
            or record["source_commit"] != RECOVERY_COMMIT or record["source_run_id"] != RECOVERY_SOURCE_RUN
            or not isinstance(record["producer_run_id"], str)
            or not re.fullmatch(r"[1-9][0-9]*", record["producer_run_id"])
            or not isinstance(record["producer_commit"], str)
            or not re.fullmatch(r"[a-f0-9]{40}", record["producer_commit"])):
        raise ValueError("recovery is not bound to the approved interrupted release")
    if source_commit is not None and source_commit != RECOVERY_COMMIT:
        raise ValueError("recovery source commit mismatch")
    if source_run_id is not None and source_run_id != RECOVERY_SOURCE_RUN:
        raise ValueError("recovery source run mismatch")
    if not isinstance(record["files"], dict) or set(record["files"]) != FILES:
        raise ValueError("invalid recovery file set")
    for name, digest in record["files"].items():
        if not isinstance(digest, str) or not DIGEST.fullmatch(digest):
            raise ValueError("invalid recovery file digest")
        path = manifest_path if name == "release-manifest.json" else directory / name
        if path.exists() and sha(path) != digest:
            raise ValueError(f"recovery content mismatch: {name}")
    manifest = json.loads(manifest_path.read_text())
    if manifest.get("release", {}).get("version") != tag or manifest["release"].get("source_commit") != RECOVERY_COMMIT:
        raise ValueError("recovered manifest source mismatch")
    return RECOVERY_IDENTITY


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--source-commit")
    parser.add_argument("--source-run-id")
    parser.add_argument("--subject", choices=("manifest", "runtime", "chart", "identity"), default="manifest")
    args = parser.parse_args()
    identity = metadata_identity(args.directory, args.tag, args.source_commit, args.source_run_id)
    if args.subject == "identity":
        print(identity)
    elif args.subject == "chart":
        verify_signature(args.directory / "release-manifest.json", args.directory / "release-manifest.sigstore.json", identity)
        manifest = json.loads((args.directory / "release-manifest.json").read_text())
        chart = args.directory / f"astronomer-{args.tag[1:]}.tgz"
        if sha(chart) != manifest["astronomer"]["chart"]["content_digest"]:
            raise ValueError("chart archive does not match manifest")
        subprocess.run(["cosign", "verify", "--certificate-identity", identity,
                        "--certificate-oidc-issuer", ISSUER, manifest["astronomer"]["chart"]["reference"]],
                       check=True, stdout=subprocess.DEVNULL)
    else:
        name = "release-manifest" if args.subject == "manifest" else "runtime-image-evidence"
        verify_signature(args.directory / f"{name}.json", args.directory / f"{name}.sigstore.json", identity)


if __name__ == "__main__":
    main()
