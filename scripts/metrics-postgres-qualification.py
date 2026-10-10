#!/usr/bin/env python3
"""Fence one exclusive PostgreSQL17/18 metrics DB before its synthetic tests."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
from urllib.parse import parse_qs, urlsplit

ROOT = Path(__file__).resolve().parents[1]
NAMESPACE = "plan032-recovery"
NAMESPACE_UID = "0883a483-0daf-4ee0-9b69-609a203cb767"
OWNER = "plan032-20261009"
LANES = {
    "17": {"cluster": "plan032-postgres", "uid": "2f83aaff-dbbf-40ef-8880-542225fec0b4", "database": "plan032_w10_metrics17"},
    "18": {"cluster": "plan032-postgres18", "uid": "ea0f410a-e1ea-4cb4-9fe5-40a35a020d3e", "database": "plan032_w10_metrics18"},
}
PARENTS = frozenset({
    "TestPostgresConnectionMetricsRepeatableReadAcrossMutation",
    "TestPostgresRegistrationMetricsRepeatableReadAcrossMutation",
    "TestPostgresVaultMetricsRepeatableReadAcrossMutation",
})


def reference(path, lane):
    path = Path(path)
    info = path.lstat()
    if path.is_symlink() or not path.is_file() or info.st_mode & 0o077:
        raise ValueError("private regular reference required")
    raw = path.read_bytes()
    document = json.loads(raw)
    if set(document) != {"database_url", "database", "cluster_uid", "primary_at_allocation"} or document["database"] != lane["database"] or document["cluster_uid"] != lane["uid"] or not isinstance(document["primary_at_allocation"], str) or not document["primary_at_allocation"]:
        raise ValueError("exclusive metrics database lease differs")
    address = urlsplit(document["database_url"])
    if address.scheme not in {"postgres", "postgresql"} or not address.hostname or not address.port or address.path != "/" + lane["database"] or address.fragment:
        raise ValueError("owned metrics endpoint differs")
    query = parse_qs(address.query, keep_blank_values=True, strict_parsing=True)
    if any(key not in {"sslmode", "sslrootcert", "connect_timeout", "application_name"} or len(values) != 1 for key, values in query.items()):
        raise ValueError("owned connection override rejected")
    return address, hashlib.sha256(raw).hexdigest()


def fence(namespace, cluster, primary, services, nodes, address, lane):
    ns = namespace["metadata"]
    if ns["name"] != NAMESPACE or ns["uid"] != NAMESPACE_UID or ns.get("labels", {}).get("astronomer.io/owner") != OWNER:
        raise ValueError("namespace ownership differs")
    metadata = cluster["metadata"]
    if metadata["name"] != lane["cluster"] or metadata["namespace"] != NAMESPACE or metadata["uid"] != lane["uid"] or metadata.get("labels", {}).get("astronomer.io/owner") != OWNER:
        raise ValueError("cluster ownership differs")
    status = cluster["status"]
    if status.get("readyInstances") != 3 or status.get("instances") != 3:
        raise ValueError("owned cluster is not fully ready")
    live = primary["metadata"]
    labels = live.get("labels", {})
    controllers = [x for x in live.get("ownerReferences", []) if x.get("controller") is True]
    if live["namespace"] != NAMESPACE or live["name"] != status["currentPrimary"] or labels.get("cnpg.io/cluster") != lane["cluster"] or labels.get("cnpg.io/instanceRole") != "primary" or len(controllers) != 1 or controllers[0].get("uid") != lane["uid"] or controllers[0].get("kind") != "Cluster":
        raise ValueError("current primary ownership differs")
    if not any(x.get("type") == "Ready" and x.get("status") == "True" for x in primary["status"].get("conditions", [])):
        raise ValueError("current primary is not ready")
    matching = [x for x in services["items"] if x["metadata"]["namespace"] == NAMESPACE and any(p.get("nodePort") == address.port and p.get("port") == 5432 for p in x["spec"].get("ports", []))]
    selector = {"cnpg.io/cluster": lane["cluster"], "cnpg.io/instanceRole": "primary"}
    if len(matching) != 1 or matching[0]["spec"].get("type") != "NodePort" or matching[0]["spec"].get("selector") != selector:
        raise ValueError("endpoint is not the unique owned primary service")
    if not any(a.get("type") == "InternalIP" and a.get("address") == address.hostname for node in nodes["items"] for a in node["status"].get("addresses", [])):
        raise ValueError("endpoint host is not an owned cluster node")
    return {"namespaceUID": ns["uid"], "clusterUID": metadata["uid"], "currentPrimary": live["name"], "currentPrimaryUID": live["uid"], "service": matching[0]["metadata"]["name"], "serviceUID": matching[0]["metadata"]["uid"], "readyInstances": 3, "database": lane["database"]}


def live_fence(args):
    lane = LANES[args.major]
    address, digest = reference(args.reference, lane)
    command = [args.kubectl, "--kubeconfig", args.kubeconfig, "--request-timeout=10s"]
    def get(kind, name=None):
        cmd = command + (["-n", NAMESPACE] if kind not in {"nodes", "namespace"} else []) + ["get", kind]
        if name:
            cmd.append(name)
        return json.loads(subprocess.check_output(cmd + ["-o", "json"], stderr=subprocess.DEVNULL, timeout=15))
    namespace = get("namespace", NAMESPACE)
    cluster = get("cluster", lane["cluster"])
    report = fence(namespace, cluster, get("pod", cluster["status"]["currentPrimary"]), get("services"), get("nodes"), address, lane)
    report.update({"observedAt": dt.datetime.now(dt.timezone.utc).isoformat(), "privateReferenceSHA256": digest, "postgresMajor": args.major})
    return report


def parent_results(path):
    terminal, failures = [], []
    for line in path.read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            return {"expected": sorted(PARENTS), "terminal": terminal, "failed": failures, "qualified": False, "error": "invalid_json_event"}
        if not isinstance(event, dict) or not isinstance(event.get("Action"), str):
            return {"expected": sorted(PARENTS), "terminal": terminal, "failed": failures, "qualified": False, "error": "invalid_json_event"}
        if event.get("Action") == "fail":
            failures.append(event.get("Test") or event.get("Package"))
        if event.get("Test") and event.get("Action") in {"pass", "fail", "skip"}:
            terminal.append((event["Test"], event["Action"]))
    passed = [name for name, action in terminal if action == "pass"]
    return {"expected": sorted(PARENTS), "terminal": terminal, "failed": failures, "qualified": not failures and len(terminal) == len(PARENTS) and len(passed) == len(PARENTS) and set(passed) == PARENTS}


def run_test_process(command, env, output, timeout=180):
    process = subprocess.Popen(command, cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
    try:
        return process.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--major", choices=sorted(LANES), required=True)
    parser.add_argument("--reference", required=True)
    parser.add_argument("--kubeconfig", required=True)
    parser.add_argument("--kubectl", default="kubectl")
    parser.add_argument("--artifact-dir", required=True)
    parser.add_argument("--run", action="store_true")
    parser.add_argument("--race", action="store_true")
    args = parser.parse_args()
    try:
        if subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT):
            raise ValueError("clean source required")
        artifact = Path(args.artifact_dir).resolve()
        if artifact == ROOT or ROOT in artifact.parents:
            raise ValueError("artifacts must remain outside source")
        artifact.mkdir(parents=True, exist_ok=False)
        if not args.run:
            report = live_fence(args)
            (artifact / "ownership-read-only.json").write_text(json.dumps(report, indent=2) + "\n")
            print("metrics qualification: live owned endpoint verified; no writes")
            return 0
        capacity = subprocess.check_output(["bash", "scripts/check-build-capacity.sh", "--json"], cwd=ROOT)
        (artifact / "capacity-before.json").write_bytes(capacity)
        # This is fresh immediately before the single mutating test process.
        report = live_fence(args)
        (artifact / "ownership-before-process.json").write_text(json.dumps(report, indent=2) + "\n")
        env = os.environ.copy()
        env.update(ASTRONOMER_METRICS_DATABASE_REF=str(Path(args.reference).resolve()), ASTRONOMER_METRICS_REFERENCE_SHA256=report["privateReferenceSHA256"], ASTRONOMER_METRICS_POSTGRES_MAJOR=args.major)
        cmd = ["go", "test", "./internal/server", "-run", "^TestPostgres(Connection|Registration|Vault)MetricsRepeatableReadAcrossMutation$", "-count=1", "-timeout=2m", "-json"] + (["-race"] if args.race else [])
        before = subprocess.check_output([sys.executable, "scripts/hash-source-tree.py", "--root", str(ROOT)], cwd=ROOT)
        with (artifact / "go-test.jsonl").open("w") as output:
            exit_code = run_test_process(cmd, env, output)
        after = subprocess.check_output([sys.executable, "scripts/hash-source-tree.py", "--root", str(ROOT)], cwd=ROOT)
        results = parent_results(artifact / "go-test.jsonl")
        receipt = {"sourceCommit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(), "sourceBefore": json.loads(before), "sourceAfter": json.loads(after), "sourceStable": before == after, "command": cmd, "exitCode": exit_code, "parents": results, "logSHA256": hashlib.sha256((artifact / "go-test.jsonl").read_bytes()).hexdigest(), "completedAt": dt.datetime.now(dt.timezone.utc).isoformat(), "scope": "Three canonical read-only RR collectors over501 synthetic rows in exclusively owned cloned schemas; no whole application/fleet qualification"}
        (artifact / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
        (artifact / "capacity-after.json").write_bytes(subprocess.check_output(["bash", "scripts/check-build-capacity.sh", "--json"], cwd=ROOT))
        return 0 if exit_code == 0 and before == after and results["qualified"] else exit_code or 1
    except (ValueError, KeyError, OSError, TypeError, subprocess.SubprocessError):
        print("metrics qualification: source, ownership or execution invariant failed", file=sys.stderr)
        return 1


if __name__ == "__main__":
    os.umask(0o077)
    sys.exit(main())
