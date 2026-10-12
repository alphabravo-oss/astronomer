#!/usr/bin/env python3
"""Retain structured test evidence and reject missing, skipped or failed cases."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[1]


def check_events(lines, expected):
    terminal, skipped, failures = {}, [], []
    for line in lines:
        event = json.loads(line)
        if not isinstance(event, dict) or not isinstance(event.get("Action"), str):
            raise ValueError("invalid Go test event")
        action, test = event["Action"], event.get("Test")
        if action == "skip":
            skipped.append(test or event.get("Package", "unknown package"))
        if action == "fail":
            failures.append(test or event.get("Package", "unknown package"))
        if test in expected and action in {"pass", "fail", "skip"}:
            if test in terminal:
                raise ValueError(f"duplicate expected test terminal event: {test}")
            terminal[test] = action
    missing = sorted(set(expected) - set(terminal))
    return {"expected": expected, "terminal": terminal, "missing": missing,
            "skipped": skipped, "failed": failures,
            "complete": not missing and not skipped and not failures
            and all(value == "pass" for value in terminal.values())}


def redact(text):
    values = set()
    for key, value in os.environ.items():
        if key.endswith("_URL"):
            try:
                password = urlsplit(value).password
                if password:
                    values.add(password)
            except ValueError:
                pass
    for value in sorted(values, key=len, reverse=True):
        text = text.replace(value, "[REDACTED]")
    return text


def build_tags(arguments):
    tags = []
    for index, value in enumerate(arguments):
        if value == "-tags":
            if index + 1 >= len(arguments):
                raise ValueError("missing build tags")
            tags.extend(arguments[index + 1].replace(",", " ").split())
        elif value.startswith("-tags="):
            tags.extend(value.removeprefix("-tags=").replace(",", " ").split())
    return sorted(set(tags))


def source_identity():
    return json.loads(subprocess.check_output([sys.executable, str(ROOT / "scripts/hash-source-tree.py"), "--root", str(ROOT)], text=True))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--name", required=True)
    parser.add_argument("--expected", action="append", required=True)
    parser.add_argument("go_args", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if len(args.expected) != len(set(args.expected)):
        parser.error("expected test names must be unique")
    if not re.fullmatch(r"[a-z][a-z0-9-]*", args.name):
        parser.error("invalid qualification name")
    command_args = args.go_args[1:] if args.go_args[:1] == ["--"] else args.go_args
    if not command_args:
        parser.error("Go test arguments are required")
    runtime = json.loads((ROOT / "deploy/qualification/runtime.json").read_text())
    gotestsum = os.environ.get("GOTESTSUM", "gotestsum")
    version = subprocess.check_output([gotestsum, "--version"], text=True).strip()
    if not re.search(r"\bv?" + re.escape(runtime["gotestsum"]) + r"\b", version):
        raise SystemExit("qualification: gotestsum version differs from the pinned authority")
    artifact_root = os.environ.get("QUALIFICATION_ARTIFACT_DIR")
    if not artifact_root:
        artifact_root = tempfile.mkdtemp(prefix="astronomer-qualification-")
    artifact = Path(artifact_root) / args.name
    artifact.mkdir(parents=True, exist_ok=False)
    # Originals may contain credentials in diagnostic errors. Keep them private,
    # outside the artifact directory uploaded by CI; publish redacted copies.
    private = Path(tempfile.mkdtemp(prefix="astronomer-private-go-evidence-"))
    original_json, original_junit = private / "go-test.json", private / "junit.xml"
    command = [gotestsum, "--format=standard-verbose", "--jsonfile", str(original_json),
               "--junitfile", str(original_junit), "--", *command_args]
    started = dt.datetime.now(dt.timezone.utc)
    source_before = source_identity()
    result = subprocess.run(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    original_log = private / "console.log"
    original_log.write_text(result.stdout)
    for path in private.iterdir():
        path.chmod(0o600)
        (artifact / path.name).write_text(redact(path.read_text()))
    contract = {"complete": False, "error": "structured JSON was not produced"}
    if original_json.exists():
        try:
            contract = check_events(original_json.read_text().splitlines(), args.expected)
        except (ValueError, json.JSONDecodeError) as error:
            contract = {"complete": False, "error": str(error)}
    now = dt.datetime.now(dt.timezone.utc)
    source_after = source_identity()
    source_stable = source_before == source_after
    source = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    document = {"schemaVersion": 1, "name": args.name, "sourceCommit": source,
                "sourceDirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT)),
                "sourceBefore": source_before, "sourceAfter": source_after, "sourceStable": source_stable,
                "startedAt": started.isoformat(), "completedAt": now.isoformat(),
                "durationSeconds": (now - started).total_seconds(), "exitCode": result.returncode,
                "status": "pass" if result.returncode == 0 and contract["complete"] and source_stable else "fail",
                "goVersion": subprocess.check_output(["go", "version"], text=True).strip(),
                "gotestsumVersion": version, "goArguments": command_args,
                "race": "-race" in command_args,
                "buildTags": build_tags(command_args),
                "postgresMajor": os.environ.get("POSTGRES_QUALIFICATION_MAJOR", runtime["defaultPostgresMajor"]),
                "operands": runtime, "testContract": contract,
                "privateOriginalDirectory": str(private),
                "originalDigests": {path.name: "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest() for path in private.iterdir()},
                "publicDigests": {path.name: "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest() for path in artifact.iterdir()}}
    (artifact / "evidence.json").write_text(json.dumps(document, indent=2) + "\n")
    print(redact(result.stdout), end="")
    print(f"qualification evidence: {artifact / 'evidence.json'}")
    return 0 if document["status"] == "pass" else result.returncode or 1


if __name__ == "__main__":
    sys.exit(main())
