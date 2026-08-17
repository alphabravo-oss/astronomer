#!/usr/bin/env python3
"""Resolve every tagged release image to an immutable registry-qualified digest."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path


DIGEST_RE = re.compile(r"^sha256:[a-f0-9]{64}$")


class ResolveError(ValueError):
    pass


def normalized_repository(reference: str) -> str:
    without_digest = reference.split("@", 1)[0]
    last = without_digest.rsplit("/", 1)[-1]
    repository = without_digest.rsplit(":", 1)[0] if ":" in last else without_digest
    parts = repository.split("/")
    if len(parts) == 1:
        return "docker.io/library/" + repository
    if "." not in parts[0] and ":" not in parts[0] and parts[0] != "localhost":
        return "docker.io/" + repository
    return repository


def inspect_digest(reference: str, command: list[str]) -> str:
    try:
        completed = subprocess.run(command + [reference], check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    except (FileNotFoundError, subprocess.CalledProcessError) as exc:
        detail = getattr(exc, "stderr", "") or ""
        raise ResolveError(f"cannot resolve image {reference!r}: {detail.strip()[:500]}") from exc
    matches = re.findall(r"(?m)^Digest:\s*(sha256:[a-f0-9]{64})\s*$", completed.stdout)
    if len(matches) != 1:
        raise ResolveError(f"resolver returned {len(matches)} digest lines for {reference!r}")
    return matches[0]


def resolve(inventory: Path, command: list[str]) -> dict[str, str]:
    result: dict[str, str] = {}
    for line in inventory.read_text(encoding="utf-8").splitlines():
        source = line.strip()
        if not source or source.startswith("#") or "@sha256:" in source:
            continue
        digest = inspect_digest(source, command)
        result[source] = normalized_repository(source) + "@" + digest
    return dict(sorted(result.items()))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inventory", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--resolver", nargs="+", default=["docker", "buildx", "imagetools", "inspect"])
    args = parser.parse_args()
    try:
        payload = resolve(args.inventory, args.resolver)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(payload, sort_keys=True, indent=2) + "\n", encoding="utf-8")
        return 0
    except (OSError, ResolveError) as exc:
        print(f"resolve-release-images: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
