#!/usr/bin/env python3
"""Reuse verified local application builds for an exact fixture-only source delta.

This is local engineering evidence, not a signed release or inherited stack.
The caller still creates fresh databases, credentials, containers and clusters.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
QUALIFICATION = Path("/root/.cache/astronomer-032/qualification")
FIXTURE_PATHS = frozenset({
    "frontend/tests/e2e-live/enterprise-journeys.live.spec.ts",
    "frontend/tests/e2e-live/refresh.live.spec.ts",
    "frontend/src/components/resources/__tests__/resource-detail-edit-actions.test.tsx",
    "scripts/test-live-browser.sh",
    "scripts/live-browser-build-reuse.py",
    "scripts/tests/live_browser_build_reuse_test.py",
    "docs/assurance/technology-guide/w12/firefox-fixture/README.md",
    "docs/assurance/technology-guide/w12/firefox-fixture/receipt.json",
})
BINARIES = ("migrator", "server", "worker", "live-browser-fixture", "flux-fixture-server")


def command(root, *args):
    return subprocess.check_output(args, cwd=root, text=True).strip()


def source_identity(root):
    spec = importlib.util.spec_from_file_location("hash_source", root / "scripts/hash-source-tree.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.tree_identity(root)


def clean(root):
    if command(root, "git", "status", "--porcelain"):
        raise ValueError("build reuse requires a clean frozen checkout")


def protected(path, scope=QUALIFICATION):
    path = Path(path)
    if not path.is_absolute() or path.is_symlink() or not path.resolve().is_relative_to(scope.resolve()):
        raise ValueError("artifact path must stay in the owned qualification directory")
    return path


def file_record(path):
    metadata = path.lstat()
    if not stat.S_ISREG(metadata.st_mode):
        raise ValueError("build artifact must be a regular file")
    return {"sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
            "size": metadata.st_size, "executable": bool(metadata.st_mode & 0o111)}


def application_files(root):
    raw = subprocess.check_output(["git", "ls-files", "-z"], cwd=root)
    result = {}
    for value in raw.split(b"\0"):
        if not value:
            continue
        name = os.fsdecode(value)
        if name in FIXTURE_PATHS:
            continue
        path = root / name
        if path.is_symlink():
            result[name] = {"kind": "symlink", "target": os.readlink(path)}
        else:
            result[name] = {"kind": "regular", **file_record(path)}
    return result


def distribution(root):
    if root.is_symlink() or not root.is_dir():
        raise ValueError("frontend build directory must be real")
    result = {}
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise ValueError("frontend build must not contain symlinks")
        if path.is_file():
            result[path.relative_to(root).as_posix()] = file_record(path)
    if "index.html" not in result or not result:
        raise ValueError("frontend build is incomplete")
    return result


def compiler_from_commit(root, commit):
    module = command(root, "git", "show", f"{commit}:go.mod")
    versions = re.findall(r"(?m)^go ([0-9]+\.[0-9]+\.[0-9]+)$", module)
    toolchains = re.findall(r"(?m)^toolchain (\S+)$", module)
    if len(versions) != 1 or len(toolchains) > 1:
        raise ValueError("application module lacks an exact compiler selection")
    compiler = toolchains[0] if toolchains else "go" + versions[0]
    if not re.fullmatch(r"go[0-9]+\.[0-9]+\.[0-9]+", compiler):
        raise ValueError("application compiler selection is not an exact stable release")
    return compiler


def bound_binary(path, commit, compiler):
    info = subprocess.check_output(["go", "version", "-m", str(path)], text=True)
    revisions = re.findall(r"(?m)^\s*build\s+vcs.revision=(\S+)\s*$", info)
    modified = re.findall(r"(?m)^\s*build\s+vcs.modified=(\S+)\s*$", info)
    if revisions != [commit] or modified != ["false"]:
        raise ValueError("application binary is not bound to the clean build commit")
    if not info.splitlines() or not info.splitlines()[0].endswith(": " + compiler):
        raise ValueError("application binary uses an unqualified compiler")
    return hashlib.sha256(info.encode()).hexdigest()


def write_json(path, value):
    with path.open("x", encoding="utf-8") as handle:
        os.chmod(path, 0o600)
        json.dump(value, handle, indent=2, sort_keys=True)
        handle.write("\n")


def capture(root, run, output, scope=QUALIFICATION):
    root = root.resolve()
    clean(root)
    commit = command(root, "git", "rev-parse", "HEAD")
    run = protected(run, scope)
    original = json.loads((run / "matrix-receipt.json").read_text())
    identity = source_identity(root)
    compiler = compiler_from_commit(root, commit)
    if original.get("sourceCommit") != commit or original.get("sourceStable") is not True or original.get("sourceBefore") != identity or original.get("sourceAfter") != identity:
        raise ValueError("original run does not bind this unchanged application source")
    artifacts = {}
    for name in BINARIES:
        path = run / "browser/bin" / name
        artifacts[name] = {"path": str(path), **file_record(path), "buildInfoSHA256": bound_binary(path, commit, compiler)}
    dist = root / "frontend/dist"
    document = {"schemaVersion": "astronomer-local-browser-build/v1", "applicationCommit": commit, "expectedCompiler": compiler, "applicationFiles": application_files(root),
                "sourceIdentity": identity, "runReceipt": {"path": str(run / "matrix-receipt.json"), **file_record(run / "matrix-receipt.json")},
                "binaries": artifacts, "frontend": {"path": str(dist), "files": distribution(dist)}}
    write_json(protected(output, scope), document)


def check(document, root, scope=QUALIFICATION):
    if set(document) != {"schemaVersion", "applicationCommit", "expectedCompiler", "applicationFiles", "sourceIdentity", "runReceipt", "binaries", "frontend"} or document["schemaVersion"] != "astronomer-local-browser-build/v1":
        raise ValueError("unsupported build reuse schema")
    commit = document["applicationCommit"]
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("invalid application commit")
    compiler = compiler_from_commit(root, commit)
    if document["expectedCompiler"] != compiler:
        raise ValueError("compiler selection differs from original application module")
    clean(root)
    command(root, "git", "merge-base", "--is-ancestor", commit, "HEAD")
    # Compare the actual checkout, not just its HEAD or declared fixture label.
    changed = set(command(root, "git", "diff", "--name-only", commit, "HEAD").splitlines())
    if changed - FIXTURE_PATHS:
        raise ValueError("application source, lock or configuration changed")
    if application_files(root) != document["applicationFiles"]:
        raise ValueError("actual application source bytes or modes changed")
    run = document["runReceipt"]
    if set(run) != {"path", "sha256", "size", "executable"} or file_record(protected(run["path"], scope)) != {k: run[k] for k in ("sha256", "size", "executable")}:
        raise ValueError("original run receipt changed")
    original = json.loads(Path(run["path"]).read_text())
    if original.get("sourceCommit") != commit or original.get("sourceStable") is not True or original.get("sourceBefore") != document["sourceIdentity"] or original.get("sourceAfter") != document["sourceIdentity"]:
        raise ValueError("original run source binding differs")
    if set(document["binaries"]) != set(BINARIES):
        raise ValueError("build binary inventory differs")
    for item in document["binaries"].values():
        if set(item) != {"path", "sha256", "size", "executable", "buildInfoSHA256"}:
            raise ValueError("unrecognized binary fields")
        path = protected(item["path"], scope)
        if file_record(path) != {k: item[k] for k in ("sha256", "size", "executable")} or not item["executable"] or bound_binary(path, commit, compiler) != item["buildInfoSHA256"]:
            raise ValueError("application binary changed")
    frontend = document["frontend"]
    # The dist lives in the original checkout, independently of private run
    # artifacts. Reject symlinks and require the exact complete byte map.
    if set(frontend) != {"path", "files"} or not Path(frontend["path"]).is_absolute() or distribution(Path(frontend["path"])) != frontend["files"]:
        raise ValueError("frontend build changed")
    return {"applicationCommit": commit, "expectedCompiler": compiler, "fixtureCommit": command(root, "git", "rev-parse", "HEAD"),
            "fixtureOnlyPaths": sorted(changed), "applicationSourceIdentity": document["sourceIdentity"],
            "fixtureSourceIdentity": source_identity(root), "qualification": "local engineering build reuse; fresh owned stack required"}


def materialize(document, root, artifact, scope=QUALIFICATION):
    evidence = check(document, root, scope)
    artifact = protected(artifact, scope)
    targets = [(Path(item["path"]), artifact / "bin" / name) for name, item in document["binaries"].items()]
    destination = root / "frontend/dist"
    if destination.exists() or destination.is_symlink() or any(target.exists() for _, target in targets):
        raise ValueError("reuse refuses to replace existing outputs")
    (artifact / "bin").mkdir(parents=True, exist_ok=True)
    for source, target in targets:
        os.link(source, target)
    shutil.copytree(document["frontend"]["path"], destination)
    # Executables are immutable linked inputs, never rebuilt in the reuse path.
    # Frontend files are separate copies so Vite cannot mutate shared bundles.
    for name, item in document["binaries"].items():
        if file_record(artifact / "bin" / name) != {k: item[k] for k in ("sha256", "size", "executable")}:
            raise ValueError("copied application binary differs")
    if distribution(destination) != document["frontend"]["files"]:
        raise ValueError("copied frontend build differs")
    evidence["binaries"] = {name: {k: item[k] for k in ("sha256", "size", "buildInfoSHA256")} for name, item in document["binaries"].items()}
    evidence["frontendFiles"] = document["frontend"]["files"]
    evidence["applicationFilesSHA256"] = hashlib.sha256(json.dumps(document["applicationFiles"], sort_keys=True).encode()).hexdigest()
    write_json(artifact / "build-reuse.json", evidence)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    capture_args = sub.add_parser("capture")
    capture_args.add_argument("--source-root", type=Path, required=True)
    capture_args.add_argument("--run-root", type=Path, required=True)
    capture_args.add_argument("--output", type=Path, required=True)
    reuse_args = sub.add_parser("reuse")
    reuse_args.add_argument("--reference", type=Path, required=True)
    reuse_args.add_argument("--artifact", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.action == "capture":
            capture(args.source_root, args.run_root, args.output)
        else:
            materialize(json.loads(protected(args.reference).read_text()), ROOT, args.artifact)
    except (ValueError, OSError, KeyError, TypeError, subprocess.CalledProcessError):
        print("live-browser build reuse: source or artifact validation failed", file=sys.stderr)
        return 1
    print("live-browser build reuse: verified")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
