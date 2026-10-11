#!/usr/bin/env python3
"""Scope Trivy exceptions to an exact verified upstream binary in a built image."""
import argparse
import datetime as dt
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
POLICY = ROOT / "deploy/release/upstream-binary-waivers.json"


def validated_ignores(policy, release, digest, now):
    if policy["release_version"] != release:
        raise ValueError("upstream binary waiver is not approved for this release")
    if dt.date.fromisoformat(policy["expires_at"]) <= now:
        raise ValueError("upstream binary waiver expired")
    if digest not in policy["binary_sha256"].values():
        raise ValueError("image contains a different upstream binary")
    if policy["component"] != "shell" or policy["path"] != "usr/local/bin/kubectl":
        raise ValueError("unexpected upstream binary waiver scope")
    return {"vulnerabilities": [
        {"id": identifier, "paths": [policy["path"]],
         "expired_at": policy["expires_at"] + "T00:00:00Z", "statement": policy["reason"]}
        for identifier in policy["ids"]
    ]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--component", required=True, choices=["server", "worker", "agent", "migrate", "shell", "dr", "frontend"])
    parser.add_argument("--image", required=True)
    parser.add_argument("--release", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    chart_version = next(line.split(":", 1)[1].strip() for line in
                         (ROOT / "deploy/chart/Chart.yaml").read_text().splitlines()
                         if line.startswith("version:"))
    if args.release != "v" + chart_version:
        raise ValueError("waiver evaluation release differs from chart identity")
    args.output.mkdir(parents=True, exist_ok=True)
    ignore = {"vulnerabilities": []}
    (args.output / "unfiltered-ignore.yaml").write_text(json.dumps(ignore) + "\n")
    evidence = {"image": args.image, "component": args.component, "release": args.release, "waivers": []}
    if args.component == "shell":
        policy = json.loads(POLICY.read_text())
        with tempfile.TemporaryDirectory() as temporary:
            container = subprocess.check_output(
                ["docker", "create", "--network", "none", args.image], text=True).strip()
            try:
                binary = Path(temporary) / "kubectl"
                subprocess.run(["docker", "cp", f"{container}:/{policy['path']}", str(binary)], check=True)
                digest = "sha256:" + hashlib.sha256(binary.read_bytes()).hexdigest()
            finally:
                subprocess.run(["docker", "rm", container], check=True, stdout=subprocess.DEVNULL)
        ignore = validated_ignores(policy, args.release, digest, dt.datetime.now(dt.timezone.utc).date())
        evidence.update({"binary_sha256": digest, "waivers": [policy]})
    (args.output / "trivyignore.yaml").write_text(json.dumps(ignore, indent=2) + "\n")
    (args.output / "waivers.json").write_text(json.dumps(evidence, indent=2) + "\n")


if __name__ == "__main__":
    main()
