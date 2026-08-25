#!/usr/bin/env python3
"""Fail closed on the exact inner upgrade and backup evidence selected by RC."""

import argparse
import hashlib
import json
import re
from pathlib import Path

DIGEST = re.compile(r"sha256:[a-f0-9]{64}\Z")
TAG = re.compile(r"v1\.[0-9]+\.[0-9]+\Z")


def digest(path: Path) -> str:
    if not path.is_file() or path.is_symlink():
        raise ValueError(f"evidence path is not a regular file: {path.name}")
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--upgrade-evidence", type=Path, required=True)
    parser.add_argument("--backup-manifest", type=Path, required=True)
    parser.add_argument("--release-manifest", type=Path, required=True)
    parser.add_argument("--target", required=True)
    parser.add_argument("--release", default="astronomer")
    args = parser.parse_args()
    if not TAG.fullmatch(args.target): raise ValueError("target must be an exact v1 tag")
    upgrade = json.loads(args.upgrade_evidence.read_text(encoding="utf-8"))
    expected = {"schema_version", "release", "namespace", "target_version", "mode", "result", "api_identity_sha256", "release_manifest_sha256", "backup_manifest_sha256", "database_restore_mode", "rollback_attempted", "completed_at"}
    if not isinstance(upgrade, dict) or set(upgrade) != expected:
        raise ValueError("inner upgrade evidence violates the closed schema")
    if upgrade["schema_version"] != 1 or upgrade["release"] != args.release or upgrade["target_version"] != args.target or upgrade["mode"] != "upgrade" or upgrade["result"] != "passed" or upgrade["database_restore_mode"] != "live_replaced" or upgrade["rollback_attempted"] is not False:
        raise ValueError("inner upgrade evidence is not an exact successful upgrade")
    for field in ("api_identity_sha256", "release_manifest_sha256", "backup_manifest_sha256"):
        if not DIGEST.fullmatch(upgrade[field]): raise ValueError(f"inner upgrade {field} is invalid")
    if upgrade["release_manifest_sha256"] != digest(args.release_manifest):
        raise ValueError("inner upgrade evidence does not bind the target release manifest")
    if upgrade["backup_manifest_sha256"] != digest(args.backup_manifest):
        raise ValueError("inner upgrade evidence does not bind the selected backup manifest")
    print(json.dumps({"upgrade_evidence_sha256": digest(args.upgrade_evidence), "backup_manifest_sha256": digest(args.backup_manifest)}, sort_keys=True))


if __name__ == "__main__":
    main()
