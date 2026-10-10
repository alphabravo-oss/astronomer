#!/usr/bin/env python3
"""Validate this unsigned W03 reader packet's immutable bindings and counters.

This checks retained consistency, not signed qualification or release approval.
Host Python is only a receipt checker, not the selected W15 runtime qualification.
"""
import copy
import hashlib
import json
from pathlib import Path
import re
import subprocess
from functools import lru_cache

ROOT = Path(__file__).resolve().parents[4]
PACKET = ROOT / "docs/assurance/technology-guide/w03/offline-database-transfer-evidence.json"


def fail_duplicates(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate receipt field")
        result[key] = value
    return result


@lru_cache(maxsize=256)
def frozen_digest(commit, path):
    data = subprocess.check_output(["git", "show", commit + ":" + path], cwd=ROOT)
    return hashlib.sha256(data).hexdigest()


def regular_local(path):
    relative = Path(path)
    if relative.is_absolute() or ".." in relative.parts:
        raise ValueError("receipt path outside owned repository")
    original = ROOT / relative
    if original.is_symlink() or any(parent.is_symlink() for parent in original.parents if parent != ROOT.parent):
        raise ValueError("receipt path is linked")
    if not original.is_file() or not original.resolve().is_relative_to(ROOT):
        raise ValueError("receipt regular file unavailable")
    return original


def counts(log):
    return {
        "parents": len(re.findall(r"^=== RUN   [^/\n]+$", log, re.M)),
        "passCases": len(re.findall(r"^\s*--- PASS:", log, re.M)),
        "skipCases": len(re.findall(r"--- SKIP:", log)),
        "failCases": len(re.findall(r"--- FAIL:", log)),
    }


def verify(packet):
    if packet["schemaVersion"] != 1 or packet["workPackage"] != "W03" or packet["slice"] != "authenticated-streamed-offline-database-reader":
        raise ValueError("wrong packet boundary")
    if packet["authoritativeQualification"] is not False or packet["wholePackageStatus"] != "IN_PROGRESS":
        raise ValueError("unsigned receipt claimed authority or whole-package completion")
    commit = packet["sourceCommit"]
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("source commit invalid")
    tree = subprocess.check_output(["git", "rev-parse", commit + "^{tree}"], cwd=ROOT, text=True).strip()
    if packet["sourceTreeGitObject"] != tree:
        raise ValueError("captured tree differs from immutable source")
    if len(packet["sourceSHA256"]) != 107:
        raise ValueError("reader source input set changed")
    for path, digest in packet["sourceSHA256"].items():
        regular_local(path)
        if not re.fullmatch(r"[0-9a-f]{64}", digest) or frozen_digest(commit, path) != digest:
            raise ValueError("immutable source binding changed")
    labels = {"focused", "race", "vet", "docs", "focused-capacity", "race-capacity", "vet-capacity", "docs-capacity", "capacity-final"}
    if len(packet["gates"]) != len(labels) or {gate["name"] for gate in packet["gates"]} != labels:
        raise ValueError("missing or duplicate gate")
    for gate in packet["gates"]:
        data = regular_local(gate["retainedLog"]).read_bytes()
        if gate["result"] != "PASS" or gate["exitCode"] != 0 or hashlib.sha256(data).hexdigest() != gate["logSHA256"]:
            raise ValueError("retained gate binding changed")
        if gate["name"] in {"focused", "race"}:
            measured = counts(data.decode("utf-8"))
            if measured != packet["observedCounts"][gate["name"]] or measured["skipCases"] or measured["failCases"]:
                raise ValueError("actual test counters changed")
    if packet["qualificationLimits"]["databaseFreshness"] != "not-assessed":
        raise ValueError("payload format test claimed DB freshness")
    if packet["qualificationLimits"]["productionStartupOIDC"] != "not qualified":
        raise ValueError("QA key claimed production OIDC")


def main():
    packet = json.loads(PACKET.read_text(), object_pairs_hook=fail_duplicates)
    verify(packet)
    mutations = []
    for field, value in [("sourceTreeGitObject", "0" * 40), ("authoritativeQualification", True), ("wholePackageStatus", "DONE")]:
        changed = copy.deepcopy(packet)
        changed[field] = value
        mutations.append(changed)
    changed = copy.deepcopy(packet)
    changed["sourceSHA256"][next(iter(changed["sourceSHA256"]))] = "0" * 64
    mutations.append(changed)
    changed = copy.deepcopy(packet)
    changed["gates"][0]["logSHA256"] = "0" * 64
    mutations.append(changed)
    changed = copy.deepcopy(packet)
    changed["observedCounts"]["race"]["passCases"] += 1
    mutations.append(changed)
    for changed in mutations:
        try:
            verify(changed)
        except ValueError:
            continue
        raise ValueError("changed binding or authority claim accepted")
    print("reader receipt consistency: 107 source bindings, 9 logs, 1 positive and 6 negative checks PASS; no qualification authority")


if __name__ == "__main__":
    main()
