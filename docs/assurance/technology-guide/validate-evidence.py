#!/usr/bin/env python3
"""Check unsigned W00 records; this never grants release or human approval."""

import datetime
import hashlib
import json
from pathlib import Path
import subprocess

import jsonschema


HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]


def load(path):
    return json.loads(path.read_text(encoding="utf-8"))


def digest(payload):
    return "sha256:" + hashlib.sha256(payload).hexdigest()


def timestamp(value):
    result = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if result.utcoffset() != datetime.timedelta(0):
        raise ValueError("timestamps must use UTC")
    return result


def validate_records(records, baseline, components, root=ROOT):
    jsonschema.Draft202012Validator(
        load(HERE / "evidence.schema.json"), format_checker=jsonschema.FormatChecker()
    ).validate(records)
    if components["sourceCommit"] != baseline["sourceCommit"]:
        raise ValueError("inventory source does not match baseline")
    catalog = next(r["commit"] for r in baseline["repositories"]
                   if r["repository"] == "astronomer-catalog")
    seen = set()
    for record in records:
        identity = (record["workPackage"], record["caseId"])
        if identity in seen:
            raise ValueError("duplicate case identity")
        seen.add(identity)
        source, execution, result = record["source"], record["execution"], record["result"]
        if source["applicationCommit"] != baseline["sourceCommit"] or source["catalogCommit"] != catalog:
            raise ValueError("record source does not match baseline")
        if source["candidateSourceTreeDigest"] != "sha256:" + baseline["candidateSourceTree"]["source_tree_sha256"]:
            raise ValueError("record tree digest does not match captured candidate")
        if timestamp(execution["finishedAt"]) < timestamp(execution["startedAt"]):
            raise ValueError("execution timestamps are reversed")
        if result["status"] == "pass":
            if execution["exitCode"] != 0 or execution["unexpectedSkips"] != 0:
                raise ValueError("pass requires zero exit and no unexpected skips")
            if execution["executedCases"] != execution["expectedCases"]:
                raise ValueError("pass requires all expected cases")
            if not result["assertions"] or not result["artifacts"]:
                raise ValueError("pass requires assertions and bound artifacts")
        if result["status"] == "fail" and execution["exitCode"] in (None, 0):
            raise ValueError("fail requires an observed nonzero exit")
        if result["status"] in {"not-run", "deferred", "not-applicable"}:
            if not result.get("reason"):
                raise ValueError("unexecuted result requires a reason")
            if result["status"] == "deferred" and not result.get("authority"):
                raise ValueError("deferral requires explicit authority")
            if execution["executedCases"] or execution["exitCode"] is not None:
                raise ValueError("unexecuted result cannot claim an exit or executed cases")
        if record["environment"]["kind"] in {"candidate-qualification", "cluster-qualification"}:
            if "manifestDigest" not in source:
                raise ValueError("candidate qualification requires manifest binding")
            if record["environment"]["kind"] == "cluster-qualification":
                if "kubernetesVersion" not in record["environment"]:
                    raise ValueError("cluster qualification requires observed Kubernetes version")
        for artifact in result["artifacts"]:
            relative = Path(artifact["path"])
            original = root / relative
            path = original.resolve()
            if relative.is_absolute() or original.is_symlink() or not path.is_relative_to(root.resolve()):
                raise ValueError("artifact must be a regular file within the repository")
            if not path.is_file() or digest(path.read_bytes()) != artifact["digest"]:
                raise ValueError("artifact digest does not match retained bytes")


def validate_source_inputs(baseline, components, root=ROOT):
    seen = set()
    for item in components["sourceInputs"]:
        if item["path"] in seen:
            raise ValueError("duplicate source input")
        seen.add(item["path"])
        # Compare the committed input, not a later executor's modified checkout.
        payload = subprocess.check_output(
            ["git", "show", baseline["sourceCommit"] + ":" + item["path"]], cwd=root
        )
        if digest(payload) != item["digest"]:
            raise ValueError("source input digest does not match committed baseline")


def main():
    baseline, components = load(HERE / "baseline.json"), load(HERE / "components.json")
    records = load(HERE / "verification.json")
    validate_records(records, baseline, components)
    validate_source_inputs(baseline, components)
    print(f"technology-guide: {len(records)} unsigned records and "
          f"{len(components['sourceInputs'])} committed inputs validated; no qualification approval implied")


if __name__ == "__main__":
    main()
