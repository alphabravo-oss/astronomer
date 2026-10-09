#!/usr/bin/env python3
"""Return a declared immutable K3s image after enforcing kubectl client skew."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def check_client(document, minor):
    if minor not in {"1.36", "1.37"}:
        raise ValueError("only owned Kubernetes1.36/1.37 lanes are declared")
    version = document.get("clientVersion", {}).get("gitVersion", "")
    match = re.fullmatch(r"v1\.([0-9]+)\.([0-9]+)(?:\+[A-Za-z0-9.-]+)?", version)
    if not match or abs(int(match[1]) - int(minor.split(".")[1])) > 1:
        raise ValueError("kubectl must be final major1 and within one minor of the selected server")
    return version


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--minor", choices=["1.36", "1.37"], default=os.environ.get("KUBERNETES_QUALIFICATION_MINOR", "1.36"))
    args = parser.parse_args()
    runtime = json.loads((ROOT / "deploy/qualification/runtime.json").read_text())
    minor = args.minor
    metadata = json.loads(subprocess.check_output(["kubectl", "version", "--client=true", "-o", "json"], text=True))
    check_client(metadata, minor)
    image = runtime["kubernetes"]["lanes"][minor]["image"]
    version = runtime["kubernetes"]["lanes"][minor]["version"]
    expected_tag = "v" + version.replace("+k3s", "-k3s")
    if not re.fullmatch(r"rancher/k3s:" + re.escape(expected_tag) + r"@sha256:[a-f0-9]{64}", image):
        raise ValueError("qualification Kubernetes image differs from exact version or lacks digest")
    print(image)


if __name__ == "__main__":
    main()
