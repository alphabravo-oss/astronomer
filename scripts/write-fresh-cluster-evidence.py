#!/usr/bin/env python3
"""Write the sanitized fresh-cluster smoke evidence manifest atomically."""

from __future__ import annotations

import argparse
import json
import os
import tempfile
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--status", required=True, choices=("pass", "fail"))
    parser.add_argument("--exit-code", required=True, type=int)
    parser.add_argument("--started-at", required=True)
    parser.add_argument("--completed-at", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--workflow", default="")
    parser.add_argument("--run-id", default="")
    parser.add_argument("--run-attempt", default="")
    parser.add_argument("--job", default="")
    parser.add_argument("--repository", default="")
    parser.add_argument("--ref", default="")
    parser.add_argument("--cluster-name", required=True)
    parser.add_argument("--cluster-id", default="")
    parser.add_argument("--kubernetes-version", default="")
    parser.add_argument("--flux-version", default="")
    parser.add_argument("--agent-image", required=True)
    parser.add_argument("--shell-image", required=True)
    parser.add_argument("--k3s-image", required=True)
    parser.add_argument("--flux-image", action="append", default=[])
    parser.add_argument("--management-image", action="append", default=[])
    parser.add_argument("--check", action="append", default=[])
    parser.add_argument("--skipped-check", action="append", default=[])
    parser.add_argument("--failed-check", default="")
    return parser.parse_args()


def write_atomic(path: Path, document: dict[str, object]) -> None:
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise ValueError("evidence output must be a regular file, not a symlink")
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=".fresh-cluster-", suffix=".json", dir=path.parent)
    try:
        os.fchmod(descriptor, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            json.dump(document, handle, indent=2, sort_keys=True)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


def main() -> int:
    args = parse_args()
    document = {
        "schema_version": "astronomer-fresh-cluster-smoke/v1",
        "status": args.status,
        "exit_code": args.exit_code,
        "started_at": args.started_at,
        "completed_at": args.completed_at,
        "source": {
            "commit": args.commit,
            "repository": args.repository,
            "ref": args.ref,
        },
        "run": {
            "workflow": args.workflow,
            "run_id": args.run_id,
            "run_attempt": args.run_attempt,
            "job": args.job,
        },
        "cluster": {
            "name": args.cluster_name,
            "id": args.cluster_id,
            "kubernetes_version": args.kubernetes_version,
        },
        "images": {
            "agent": args.agent_image,
            "shell": args.shell_image,
            "k3s": args.k3s_image,
            "flux_controllers": sorted(set(args.flux_image)),
            "management_plane": sorted(set(args.management_image)),
        },
        "flux": {
            "distribution_version": args.flux_version,
        },
        "checks": (
            [{"id": check, "status": "pass"} for check in args.check]
            + [{"id": check, "status": "skip"} for check in args.skipped_check]
        ),
        "failed_check": args.failed_check if args.status == "fail" else "",
    }
    write_atomic(args.output, document)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
