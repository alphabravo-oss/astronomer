#!/usr/bin/env python3
"""Aggregate the four signed production scale rungs for one exact release."""

import argparse
import datetime as dt
import hashlib
import json
from pathlib import Path

PROFILES = {"estate-100", "estate-500", "estate-1000", "estate-1000-soak"}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input-dir", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--sizing", type=Path, required=True)
    parser.add_argument("--sizing-docs-table", type=Path, required=True)
    parser.add_argument("--target-version", required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--source-run-id", required=True)
    args = parser.parse_args()
    if not __import__("re").fullmatch(r"v1\.[0-9]+\.[0-9]+", args.target_version):
        raise SystemExit("target version must be an exact v1 tag")
    if not __import__("re").fullmatch(r"[a-f0-9]{40}", args.source_commit) or not __import__("re").fullmatch(r"[1-9][0-9]*", args.source_run_id):
        raise SystemExit("aggregate source commit/run identity is invalid")
    manifests = sorted(args.input_dir.rglob("evidence-manifest.json"))
    if len(manifests) != len(PROFILES):
        raise SystemExit(f"expected exactly four production manifests, found {len(manifests)}")
    rows = []
    release = None
    seen = set()
    for path in manifests:
        body = path.read_bytes()
        manifest = json.loads(body)
        profile = manifest.get("profile")
        if profile not in PROFILES or profile in seen or manifest.get("verdict") != "pass":
            raise SystemExit(f"invalid or duplicate production rung: {profile}")
        seen.add(profile)
        current_release = manifest.get("release")
        if release is None:
            release = current_release
        elif current_release != release:
            raise SystemExit("production rungs do not bind the same release and environment")
        bundle = path.with_name("evidence-manifest.sigstore.json")
        baseline = path.with_name("baseline-row.json")
        if not bundle.is_file() or not baseline.is_file():
            raise SystemExit(f"rung {profile} is missing signature or baseline evidence")
        rows.append({
            "profile": profile,
            "manifest_sha256": hashlib.sha256(body).hexdigest(),
            "signature_bundle_sha256": hashlib.sha256(bundle.read_bytes()).hexdigest(),
            "baseline": json.loads(baseline.read_text(encoding="utf-8")),
        })
    if seen != PROFILES:
        raise SystemExit(f"production rung set is incomplete: {sorted(PROFILES - seen)}")
    sizing = json.loads(args.sizing.read_text(encoding="utf-8"))
    if sizing.get("schema_version") != "astronomer-component-sizing-recommendations-v1" or sizing.get("release") != release:
        raise SystemExit("component sizing evidence does not bind the aggregated release")
    output = {
        "schema_version": "astronomer-scale-certification-set-v1",
        "target_version": args.target_version,
        "source_commit": args.source_commit,
        "source_run_id": args.source_run_id,
        "completed_at": dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "release": release,
        "profiles": sorted(rows, key=lambda row: row["profile"]),
        "component_sizing": {
            "recommendations_sha256": hashlib.sha256(args.sizing.read_bytes()).hexdigest(),
            "docs_table_sha256": hashlib.sha256(args.sizing_docs_table.read_bytes()).hexdigest(),
            "status": sizing.get("status"),
        },
    }
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(output, indent=2, sort_keys=True) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
