#!/usr/bin/env python3
"""Check support claims and execute only source-owned disposable qualification lanes."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
MATRIX = ROOT / "deploy/qualification/support-matrix.json"


def commands():
    result = {}
    definitions = {
        "postgres": ("test-postgres-integration.sh", "POSTGRES_INTEGRATION_RACE"),
        "worker": ("test-worker-runtime-integration.sh", "WORKER_INTEGRATION_RACE"),
        "process-restart": ("test-process-restart-qualification.sh", "PROCESS_RESTART_QUALIFICATION_RACE"),
        "redis-outage": ("test-redis-outage-recovery.sh", "REDIS_OUTAGE_RECOVERY_RACE"),
        "postgres-outage": ("test-postgres-outage-qualification.sh", "POSTGRES_OUTAGE_QUALIFICATION_RACE"),
        "streaming": ("test-postgres-failover-certification.sh", "POSTGRES_FAILOVER_CERTIFICATION_RACE"),
    }
    for name, (script, race_key) in definitions.items():
        for major in ("17", "18"):
            result[name + major] = (["bash", "scripts/" + script], {"POSTGRES_QUALIFICATION_MAJOR": major})
            if race_key:
                result[name + major + "-race"] = (["bash", "scripts/" + script], {"POSTGRES_QUALIFICATION_MAJOR": major, race_key: "1"})
    result["migrations"] = (["bash", "scripts/migrate-roundtrip-smoke.sh"], {})
    result["components"] = (["npm", "--prefix", "frontend", "run", "test:browser-components", "--", "--workers=1", "--retries=0"], {})
    for engine in ("chromium", "firefox", "webkit"):
        result["live-" + engine] = (["bash", "scripts/test-live-browser.sh"], {"LIVE_BROWSER_ENGINE": engine})
    return result


def validate(document, root=ROOT, now=None):
    now = now or dt.datetime.now(dt.timezone.utc)
    observed = dt.datetime.fromisoformat(document["observedAt"].replace("Z", "+00:00"))
    if observed.tzinfo is None or observed > now:
        raise ValueError("matrix discovery must have an actual non-future timezone")
    if dt.date.fromisoformat(document["reviewBy"]) <= now.date():
        raise ValueError("support-matrix review expired")
    inventory = document["offeringInventory"]
    if hashlib.sha256((root / inventory["path"]).read_bytes()).hexdigest() != inventory["sha256"]:
        raise ValueError("offering inventory changed; reconcile its full denominator")
    if set(document["lanes"]) != set(commands()):
        raise ValueError("matrix lane inventory differs from source-owned commands")
    seen = set()
    for claim in document["claims"]:
        if claim["id"] in seen:
            raise ValueError("duplicate support claim")
        seen.add(claim["id"])
        path = claim["source"].split("#", 1)[0]
        if not path or path.startswith("/") or ".." in Path(path).parts or not (root / path).exists():
            raise ValueError("support claim lacks a current source")
        if not claim["owner"].strip() or not claim["scope"].strip():
            raise ValueError("support claim lacks an owner or precise scope")
        if dt.date.fromisoformat(claim["reviewBy"]) <= now.date():
            raise ValueError("support claim review expired")
        if claim["classification"] == "exercised_locally" and not claim["evidence"]:
            raise ValueError("local exercise requires attributable evidence")
        for proof in claim["evidence"]:
            path = proof["path"]
            if path.startswith("/") or ".." in Path(path).parts or hashlib.sha256((root / path).read_bytes()).hexdigest() != proof["sha256"]:
                raise ValueError("support evidence bytes differ")
        if claim["classification"] == "externally_qualified":
            raise ValueError("this matrix has no approved external qualification evidence")
    return document


def run(lane, artifact, document):
    if lane not in commands() or lane not in document["lanes"]:
        raise ValueError("unknown or unowned qualification lane")
    if subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT):
        raise ValueError("qualification requires a clean immutable checkout")
    artifact = Path(artifact).resolve()
    if artifact == ROOT or ROOT in artifact.parents:
        raise ValueError("qualification artifacts must be outside the frozen source")
    artifact.mkdir(parents=True, exist_ok=False, mode=0o700)
    command, lane_env = commands()[lane]
    source = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    hash_command = [sys.executable, "scripts/hash-source-tree.py", "--root", str(ROOT)]
    before = json.loads(subprocess.check_output(hash_command, cwd=ROOT, text=True))
    env = os.environ.copy()
    # A parent invocation cannot quietly select a different destructive DB/image,
    # enable retries, or reuse another lane's output or browser engine.
    for key in list(env):
        if key.endswith(("_DATABASE_URL", "_RACE")) or key in {"DATABASE_URL", "POSTGRES_FAILOVER_IMAGE", "K3S_IMAGE", "POSTGRES_QUALIFICATION_MAJOR", "LIVE_BROWSER_ENGINE", "QUALIFICATION_ARTIFACT_DIR", "LIVE_BROWSER_ARTIFACT_DIR", "POSTGRES_FAILOVER_ARTIFACT_DIR"}:
            env.pop(key)
    env.update(lane_env)
    env.update({"QUALIFICATION_ARTIFACT_DIR": str(artifact / "go"), "LIVE_BROWSER_ARTIFACT_DIR": str(artifact / "browser"), "POSTGRES_FAILOVER_ARTIFACT_DIR": str(artifact / "streaming")})
    started = dt.datetime.now(dt.timezone.utc)
    log = artifact / "runner.log"
    with log.open("w") as handle:
        result = subprocess.run(command, cwd=ROOT, env=env, stdout=handle, stderr=subprocess.STDOUT)
    log.chmod(0o600)
    after = json.loads(subprocess.check_output(hash_command, cwd=ROOT, text=True))
    finished = dt.datetime.now(dt.timezone.utc)
    report = {"schemaVersion": 1, "lane": lane, "sourceCommit": source, "sourceBefore": before, "sourceAfter": after, "sourceStable": before == after, "command": command, "laneEnvironment": lane_env, "startedAt": started.isoformat(), "completedAt": finished.isoformat(), "durationSeconds": (finished-started).total_seconds(), "exitCode": result.returncode, "status": "pass" if result.returncode == 0 and before == after else "fail", "runnerLogSHA256": hashlib.sha256(log.read_bytes()).hexdigest(), "scope": "Source-owned disposable lane only; excludes external qualification and universal support/SLA claims."}
    (artifact / "matrix-receipt.json").write_text(json.dumps(report, indent=2) + "\n")
    print(f"{lane}: {report['status']}; receipt {artifact / 'matrix-receipt.json'}")
    return 0 if report["status"] == "pass" else result.returncode or 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--run", choices=sorted(commands()))
    parser.add_argument("--artifact-dir")
    args = parser.parse_args()
    subprocess.run(["go", "run", "./deploy/toolchain/check", "deploy/qualification/support-matrix.schema.json", str(MATRIX)], cwd=ROOT, check=True)
    document = validate(json.loads(MATRIX.read_text()))
    if args.run:
        if not args.artifact_dir:
            parser.error("--run requires a new --artifact-dir outside source")
        return run(args.run, args.artifact_dir, document)
    print(f"Support matrix: {len(document['claims'])} scoped claims; {len(document['lanes'])} disposable lanes; no external qualification asserted")
    return 0


if __name__ == "__main__":
    sys.exit(main())
