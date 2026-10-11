#!/usr/bin/env python3
"""Produce an exact, dated oasdiff ignore file without editing history."""
import argparse
import datetime as dt
import hashlib
import json
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
IDENTITY = ("id", "text", "level", "operation", "operationId", "path")
ENTRY_KEYS = set(IDENTITY) | {"ignoreLine", "owner", "reviewedAt", "expiresAt", "evidence"}
REGISTRY_KEYS = {"schemaVersion", "sourceSpecificationDigest", "baselineSpecifications", "historicalReviewDigest", "historicalBoundary", "entries"}


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def instant(value):
    timestamp = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if timestamp.tzinfo is None or timestamp.utcoffset() != dt.timedelta(0):
        raise ValueError("review dates must be explicitly UTC")
    return timestamp


def identity(row):
    return tuple(row[key] for key in IDENTITY)


def effective_review(registry, review, report, source_digest, base_digest, now):
    if set(registry) != REGISTRY_KEYS or registry["schemaVersion"] != 1:
        raise ValueError("unsupported or unclosed current-review registry")
    if not isinstance(report, list) or not all(isinstance(row, dict) for row in report):
        raise ValueError("invalid raw oasdiff report")
    boundary = registry["historicalBoundary"]
    if not boundary or review.count(boundary) != 1:
        raise ValueError("historical review boundary is not unique")
    historical, current = review.split(boundary, 1)
    if digest(historical.encode()) != registry["historicalReviewDigest"]:
        raise ValueError("historical review bytes changed")
    baselines = registry["baselineSpecifications"]
    if not baselines or any(set(row) != {"revision", "digest"} for row in baselines):
        raise ValueError("invalid approved baseline binding")
    approved_bases = {row["digest"] for row in baselines}
    entries, lines = {}, set()
    for row in registry["entries"]:
        if set(row) != ENTRY_KEYS:
            raise ValueError("unclosed review entry")
        key = identity(row)
        if key in entries or row["ignoreLine"] in lines:
            raise ValueError("duplicate current review")
        if row["level"] not in (2, 3) or row["operation"] not in {"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE"} or not row["path"].startswith("/api/v1/"):
            raise ValueError("invalid exact review identity")
        expected = f'- {row["operation"]} {row["path"]} {row["text"]}'
        if row["ignoreLine"].replace("\\\\", "\\") != expected:
            raise ValueError("ignore line does not match exact report identity")
        reviewed, expires = instant(row["reviewedAt"]), instant(row["expiresAt"])
        if expires <= reviewed or reviewed > now:
            raise ValueError("invalid review date interval")
        if not row["owner"].strip() or not row["evidence"].startswith("docs/assurance/technology-guide/"):
            raise ValueError("review must have a specific owner and evidence")
        entries[key] = (row, expires)
        lines.add(row["ignoreLine"])
    documented = [line for line in current.splitlines() if line.startswith("- ")]
    if len(documented) != len(lines) or set(documented) != lines:
        raise ValueError("current prose review and registry differ")
    matched = []
    for warning in report:
        if not all(key in warning for key in IDENTITY):
            raise ValueError("raw report lacks an exact warning identity")
        entry = entries.get(identity(warning))
        if entry is None:
            # Leave unrelated changes to the existing exact historical policy
            # and oasdiff failure gate. No current review is passed for them.
            continue
        row, expires = entry
        if source_digest != registry["sourceSpecificationDigest"] or base_digest not in approved_bases:
            raise ValueError("current review is not bound to this source and baseline specification")
        if now >= expires:
            raise ValueError(f'current review expired: {row["operation"]} {row["path"]} {row["id"]}')
        matched.append(row["ignoreLine"])
    if len(matched) != len(set(matched)):
        raise ValueError("raw report repeats a current review warning")
    return historical + "\n<!-- Executable current reviews; exact matched, unexpired rows only. -->\n\n" + "\n".join(matched) + "\n"


def specification_bytes(reference):
    file = ROOT / reference
    if file.is_file():
        return file.read_bytes()
    if ":" not in reference or reference.startswith(("http:", "https:")):
        raise ValueError("baseline must be a local file or immutable Git specification")
    return subprocess.check_output(["git", "show", reference], cwd=ROOT)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    registry = json.loads((ROOT / "deploy/contracts/openapi-current-reviews.json").read_text())
    review = (ROOT / "docs/api-breaking-change-review.md").read_text()
    result = effective_review(registry, review, json.loads(args.report.read_text()),
                              digest(specification_bytes(args.revision)), digest(specification_bytes(args.baseline)),
                              dt.datetime.now(dt.timezone.utc))
    args.output.write_text(result)
    print("OpenAPI current reviews: exact identities, specification bindings and expiry verified")


if __name__ == "__main__":
    main()
