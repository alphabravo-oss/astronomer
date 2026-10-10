#!/usr/bin/env python3
"""Check retained local source/log bindings; this does not approve a release."""
import copy
import hashlib
import functools
import json
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[4]
DIRECTORY = Path(__file__).resolve().parent
sha = lambda data: hashlib.sha256(data).hexdigest()


def closed(value, keys):
    if not isinstance(value, dict) or set(value) != set(keys):
        raise ValueError("closed evidence shape differs")


def owned(path):
    original = ROOT / path
    if Path(path).is_absolute() or original.is_symlink():
        raise ValueError("evidence path is not an owned regular file")
    resolved = original.resolve()
    if not resolved.is_relative_to(ROOT) or not resolved.is_file():
        raise ValueError("evidence path escapes source root")
    return resolved


def events(path):
    rows = []
    for line in owned(path).read_text().splitlines():
        try:
            row = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(row, dict):
            rows.append(row)
    counts = {action: sum(row.get("Action") == action and bool(row.get("Test")) for row in rows) for action in ("pass", "fail", "skip")}
    parents = sum(row.get("Action") == "pass" and bool(row.get("Test")) and "/" not in row["Test"] for row in rows)
    return rows, counts, parents


@functools.cache
def git_source_digest(commit, path):
    return sha(subprocess.check_output(["git", "show", commit + ":" + path], cwd=ROOT))


@functools.cache
def expected_source_paths(commit):
    names = subprocess.check_output(["git", "ls-tree", "-r", "--name-only", commit], cwd=ROOT, text=True).splitlines()
    exact = {"go.mod", "go.sum", "docs/data-governance/json-contracts.json", "scripts/check-json-governance.py", "scripts/check-dependency-boundaries.mjs", "scripts/check-complexity-budget.mjs", "scripts/check-build-capacity.sh", "frontend/package.json", "frontend/package-lock.json"}
    return {name for name in names if name.endswith(".go") or name in exact or name.startswith("internal/") and name.endswith(".json")}


def bound_receipt(binding, corrected):
    closed(binding, ("path", "sha256"))
    data = owned(binding["path"]).read_bytes()
    if sha(data) != binding["sha256"]:
        raise ValueError("raw receipt hash differs")
    receipt = json.loads(data)
    closed(receipt, ("schemaVersion", "workPackage", "slice", "wholePackageStatus", "authoritativeQualification", "sourceBefore", "environment", "nodeBinary", "cosignSHA256", "gates", "supplierWholeNetworkLayerTransfer", "productionOIDCPublication", "measuredScanUsedDB", "durableProjection", "readonlyDocumentationDependencyLockMatch", "effectiveGo", "sourceAfter", "sourceBeforeAfterByteEqual", "counts"))
    if type(receipt["schemaVersion"]) is not int or receipt["schemaVersion"] != 1 or receipt["workPackage"] != "W04" or receipt["readonlyDocumentationDependencyLockMatch"] is not True or receipt["nodeBinary"] != "/root/.cache/astronomer-032/toolchains/node-v26.11.1-linux-x64/bin/node":
        raise ValueError("receipt/tool shape differs")
    expected_commit = "45ee1c2e0ad5e88a82202726a0621b4e2ba44203" if corrected else "bd4ba64c546cc716151d5681f79bac5683e05567"
    before, after = receipt["sourceBefore"], receipt["sourceAfter"]
    closed(before, ("commit", "tree", "sourceSHA256"))
    closed(after, ("commit", "tree", "sourceSHA256"))
    if before != after or receipt["sourceBeforeAfterByteEqual"] is not True or before["commit"] != expected_commit:
        raise ValueError("source changed or commit differs")
    tree = subprocess.check_output(["git", "rev-parse", expected_commit + "^{tree}"], cwd=ROOT, text=True).strip()
    if before["tree"] != tree or set(before["sourceSHA256"]) != expected_source_paths(expected_commit):
        raise ValueError("captured tree or source set differs")
    for path, digest in before["sourceSHA256"].items():
        if git_source_digest(expected_commit, path) != digest:
            raise ValueError("source hash differs from retained Git object")
    if receipt["effectiveGo"] != "go version go1.27.2 linux/amd64" or receipt["authoritativeQualification"] is not False or receipt["wholePackageStatus"] != "IN_PROGRESS":
        raise ValueError("compiler or qualification scope differs")
    expected_environment = {"GOTOOLCHAIN": "go1.27.2", "GOMAXPROCS": "4", "GOFLAGS": "-p=2", "COSIGN_BIN": "/root/.cache/astronomer-032/toolchains/bin/cosign-3.1.3", "NODE_PATH": "/root/astronomer-all/astronomer-032-w07/frontend/node_modules"}
    if receipt["environment"] != expected_environment or receipt["cosignSHA256"] != "4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71" or sha(Path(expected_environment["COSIGN_BIN"]).read_bytes()) != receipt["cosignSHA256"]:
        raise ValueError("selected tools differ")
    for flag in ("supplierWholeNetworkLayerTransfer", "productionOIDCPublication", "measuredScanUsedDB", "durableProjection"):
        if receipt[flag] is not False:
            raise ValueError("unperformed qualification asserted")
    commands = {
        "focused-race": ["go", "test", "-race", "-json", "./internal/delivery/distribution", "./internal/delivery/resolver", "-run", "^(TestDatabaseRegistry.*|TestSupplier.*Transfer.*)$", "-count=1"],
        "full-race": ["go", "test", "-race", "-json", "./internal/delivery/distribution", "./internal/delivery/resolver", "-count=1"],
        "vet": ["go", "vet", "./internal/delivery/distribution", "./internal/delivery/resolver"],
        "json-governance": ["python3", "scripts/check-json-governance.py"],
        "boundary": ["node", "scripts/check-dependency-boundaries.mjs"],
        "complexity": ["node", "scripts/check-complexity-budget.mjs"],
        "docs": ["make", "docs-check"],
    }
    for name in list(commands):
        commands[name + "-capacity"] = ["bash", "scripts/check-build-capacity.sh"]
    commands["capacity-final"] = ["bash", "scripts/check-build-capacity.sh"]
    gates = receipt["gates"]
    if len(gates) != 15 or {gate["name"] for gate in gates} != set(commands):
        raise ValueError("gate set differs")
    for gate in gates:
        closed(gate, ("name", "command", "exitCode", "seconds", "retainedLog", "logSHA256"))
        expected_exit = 0 if corrected or gate["name"] not in ("focused-race", "full-race", "json-governance") else 1
        if type(gate["exitCode"]) is not int or gate["exitCode"] != expected_exit or gate["command"] != commands[gate["name"]] or sha(owned(gate["retainedLog"]).read_bytes()) != gate["logSHA256"]:
            raise ValueError("gate command/result/hash differs")
    for name, expected in (("focused-race", (50, 0, 9) if corrected else (31, 19, 4)), ("full-race", (345, 0, 85) if corrected else (326, 19, 80))):
        closed(receipt["counts"], ("focused-race", "full-race"))
        closed(receipt["counts"][name], ("actions", "parentPasses", "packageResults", "selectedCosignFixture"))
        gate = next(g for g in gates if g["name"] == name)
        rows, counts, parents = events(gate["retainedLog"])
        if counts != {"pass": expected[0], "fail": expected[1], "skip": 0} or parents != expected[2] or receipt["counts"][name]["actions"] != counts or receipt["counts"][name]["parentPasses"] != parents:
            raise ValueError("raw race event counts differ")
        packages = [r for r in rows if r.get("Action") in ("pass", "fail", "skip") and not r.get("Test")]
        actual_packages = [{"package": r.get("Package"), "action": r.get("Action"), "seconds": r.get("Elapsed")} for r in packages]
        if receipt["counts"][name]["packageResults"] != actual_packages or len(packages) != 2 or {r["Package"] for r in packages} != {"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution", "github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"} or (corrected and any(r["Action"] != "pass" for r in packages)):
            raise ValueError("package execution differs")
        if name == "full-race":
            actual = [r for r in rows if r.get("Test") == "TestSelectedCosignDatabaseUpdateToCanonicalLoader" and r.get("Action") in ("pass", "fail", "skip")]
            if len(actual) != 1 or actual[0]["Action"] != "pass" or receipt["counts"][name]["selectedCosignFixture"] != actual:
                raise ValueError("actual selected Cosign fixture missing")
    return receipt


def validate(index):
    closed(index, ("schemaVersion", "kind", "scope", "authoritativeQualification", "sourceCommit", "sourceTree", "baseline", "corrected", "readerBinding", "limits"))
    if type(index["schemaVersion"]) is not int or index["schemaVersion"] != 1 or index["kind"] != "RetainedSupplierTransferEvidence" or index["scope"] != "source-fixtures-only" or index["authoritativeQualification"] is not False:
        raise ValueError("local evidence scope invalid")
    final = bound_receipt(index["corrected"], True)
    bound_receipt(index["baseline"], False)
    if index["sourceCommit"] != final["sourceBefore"]["commit"] or index["sourceTree"] != final["sourceBefore"]["tree"]:
        raise ValueError("candidate source tree differs")
    expected_limits = {"supplierWholeNetworkLayerTransfer": False, "productionOIDCPublication": False, "measuredScanUsedDB": False, "durableProjection": False, "wholeW04Complete": False}
    if index["limits"] != expected_limits or any(value is not False for value in index["limits"].values()):
        raise ValueError("unperformed qualification promoted")
    reader = index["readerBinding"]
    closed(reader, ("path", "function", "moduleSHA256", "functionSHA256"))
    if reader["path"] != "internal/delivery/resolver/database_registry_auth.go" or reader["function"] != "boundDatabaseTokenResponse":
        raise ValueError("reader binding differs")
    source = subprocess.check_output(["git", "show", index["sourceCommit"] + ":" + reader["path"]], cwd=ROOT)
    declaration = source[source.index(b"func boundDatabaseTokenResponse("):]
    if sha(source) != reader["moduleSHA256"] or sha(declaration) != reader["functionSHA256"]:
        raise ValueError("actual token reader source differs")


def main():
    index = json.loads((DIRECTORY / "supplier-transfer-evidence.json").read_text())
    validate(index)
    negatives = []
    for field, value in (("schemaVersion", 2), ("schemaVersion", True), ("sourceTree", "0" * 40), ("sourceCommit", "0" * 40), ("authoritativeQualification", True), ("scope", "supplier-live")):
        changed = copy.deepcopy(index); changed[field] = value; negatives.append(changed)
    changed = copy.deepcopy(index); changed["unknown"] = True; negatives.append(changed)
    changed = copy.deepcopy(index); changed["limits"]["wholeW04Complete"] = True; negatives.append(changed)
    changed = copy.deepcopy(index); changed["readerBinding"]["functionSHA256"] = "0" * 64; negatives.append(changed)
    changed = copy.deepcopy(index); changed["corrected"]["sha256"] = "0" * 64; negatives.append(changed)
    changed = copy.deepcopy(index); changed["corrected"]["path"] = "../outside"; negatives.append(changed)
    # Rehash mutated receipts: an outer digest alone cannot accept altered source,
    # commands, counts or a missing actual selected-tool fixture.
    original = json.loads(owned(index["corrected"]["path"]).read_text())
    with tempfile.TemporaryDirectory(prefix="transfer-check-", dir=DIRECTORY) as temporary:
        modifications = [lambda r: r.update({"unknown": True}), lambda r: r["sourceAfter"]["sourceSHA256"].update({next(iter(r["sourceAfter"]["sourceSHA256"])): "0" * 64}), lambda r: r["gates"][1].update({"command": ["true"]}), lambda r: r["counts"]["full-race"].update({"selectedCosignFixture": []}), lambda r: r["counts"]["full-race"]["actions"].update({"pass": 999})]
        for number, modify in enumerate(modifications):
            altered = copy.deepcopy(original); modify(altered)
            path = Path(temporary) / f"receipt-{number}.json"; data = (json.dumps(altered) + "\n").encode(); path.write_bytes(data)
            changed = copy.deepcopy(index); changed["corrected"] = {"path": str(path.relative_to(ROOT)), "sha256": sha(data)}; negatives.append(changed)
        link = Path(temporary) / "linked.json"; link.symlink_to(owned(index["corrected"]["path"]))
        changed = copy.deepcopy(index); changed["corrected"]["path"] = str(link.relative_to(ROOT)); negatives.append(changed)
        for changed in negatives:
            try:
                validate(changed)
            except (ValueError, KeyError, subprocess.CalledProcessError):
                continue
            raise ValueError("negative evidence unexpectedly accepted")
    print(f"supplier-transfer evidence: 1 positive + {len(negatives)} negative assertions PASS; local evidence only")


if __name__ == "__main__":
    main()
