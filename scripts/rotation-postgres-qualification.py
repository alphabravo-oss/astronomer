#!/usr/bin/env python3
"""Fence one exclusively leased rotation DB before its mutating test process."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
from urllib.parse import parse_qs, urlsplit

ROOT = Path(__file__).resolve().parents[1]
CLUSTER = "plan032-postgres18"
CLUSTER_UID = "ea0f410a-e1ea-4cb4-9fe5-40a35a020d3e"
OWNER = "plan032-20261009"
DATABASE = "plan032_w16_rotation"
NAMESPACE = "plan032-recovery"


def private_reference(path):
    path = Path(path)
    info = path.lstat()
    if path.is_symlink() or not path.is_file() or info.st_mode & 0o077:
        raise ValueError("private regular reference required")
    raw = path.read_bytes()
    document = json.loads(raw)
    if set(document) != {"database_url", "database", "cluster_uid", "primary_at_allocation"} or document["database"] != DATABASE or document["cluster_uid"] != CLUSTER_UID:
        raise ValueError("exclusive rotation lease required")
    address = urlsplit(document["database_url"])
    if address.scheme not in {"postgres", "postgresql"} or not address.hostname or not address.port or address.path != "/" + DATABASE or address.fragment:
        raise ValueError("invalid owned endpoint")
    query = parse_qs(address.query, keep_blank_values=True, strict_parsing=True)
    if any(key not in {"sslmode", "sslrootcert", "connect_timeout", "application_name"} or len(values) != 1 for key, values in query.items()):
        raise ValueError("connection override rejected")
    return address, hashlib.sha256(raw).hexdigest()


def fence(cluster, primary, services, nodes, address):
    metadata = cluster["metadata"]
    if metadata["name"] != CLUSTER or metadata["namespace"] != NAMESPACE or metadata["uid"] != CLUSTER_UID or metadata.get("labels", {}).get("astronomer.io/owner") != OWNER:
        raise ValueError("live cluster ownership differs")
    if cluster["status"].get("readyInstances") != 3:
        raise ValueError("owned cluster is not fully ready")
    live = primary["metadata"]
    labels = live.get("labels", {})
    owner = [x for x in live.get("ownerReferences", []) if x.get("controller") is True]
    if live["namespace"] != NAMESPACE or live["name"] != cluster["status"]["currentPrimary"] or labels.get("cnpg.io/cluster") != CLUSTER or labels.get("cnpg.io/instanceRole") != "primary" or len(owner) != 1 or owner[0].get("uid") != CLUSTER_UID or owner[0].get("kind") != "Cluster":
        raise ValueError("current primary owner differs")
    if not any(x.get("type") == "Ready" and x.get("status") == "True" for x in primary["status"].get("conditions", [])):
        raise ValueError("current primary is not ready")
    matching = [x for x in services["items"] if x["metadata"]["namespace"] == NAMESPACE and any(p.get("nodePort") == address.port and p.get("port") == 5432 for p in x["spec"].get("ports", []))]
    if len(matching) != 1 or matching[0]["spec"].get("type") != "NodePort" or matching[0]["spec"].get("selector") != {"cnpg.io/cluster": CLUSTER, "cnpg.io/instanceRole": "primary"}:
        raise ValueError("leased endpoint is not the owned primary service")
    if not any(a.get("type") == "InternalIP" and a.get("address") == address.hostname for node in nodes["items"] for a in node["status"].get("addresses", [])):
        raise ValueError("leased endpoint host is not an owned cluster node")
    return {"clusterUID": metadata["uid"], "currentPrimary": live["name"], "currentPrimaryUID": live["uid"], "service": matching[0]["metadata"]["name"], "serviceUID": matching[0]["metadata"]["uid"], "readyInstances": 3, "database": DATABASE}


def live_fence(args):
    address, digest = private_reference(args.reference)
    command = [args.kubectl, "--kubeconfig", args.kubeconfig, "--request-timeout=10s"]
    def get(kind, name=None):
        cmd = command + (["-n", NAMESPACE] if kind != "nodes" else []) + ["get", kind]
        if name:
            cmd.append(name)
        return json.loads(subprocess.check_output(cmd + ["-o", "json"], stderr=subprocess.DEVNULL, timeout=15))
    cluster = get("cluster", CLUSTER)
    report = fence(cluster, get("pod", cluster["status"]["currentPrimary"]), get("services"), get("nodes"), address)
    report.update({"observedAt": dt.datetime.now(dt.timezone.utc).isoformat(), "privateReferenceSHA256": digest})
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
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
            raise ValueError("evidence must be outside source")
        artifact.mkdir(mode=0o700, parents=True, exist_ok=False)
        report = live_fence(args)
        (artifact / "ownership-fence.json").write_text(json.dumps(report, indent=2) + "\n")
        if args.run:
            capacity = subprocess.check_output(["bash", "scripts/check-build-capacity.sh", "--json"], cwd=ROOT)
            (artifact / "capacity-before.json").write_bytes(capacity)
            # Re-fence immediately before every mutating test process, after any
            # preparation. No mutation command accepts the DSN as an argument.
            report = live_fence(args)
            (artifact / "ownership-before-process.json").write_text(json.dumps(report, indent=2) + "\n")
            env = os.environ.copy()
            env["ASTRONOMER_ROTATION_DATABASE_REF"] = str(Path(args.reference).resolve())
            cmd = ["go", "test", "./cmd/keyrotate", "-run", "^TestPostgresRotation", "-count=1", "-json"] + (["-race"] if args.race else [])
            with (artifact / "go-test.jsonl").open("w") as output:
                completed = subprocess.run(cmd, cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT, timeout=180)
            return completed.returncode
        print("rotation qualification: owned live cluster, primary and service endpoint verified; no writes")
        return 0
    except (ValueError, KeyError, OSError, TypeError, subprocess.SubprocessError):
        print("rotation qualification: source, ownership or execution invariant failed", file=sys.stderr)
        return 1


if __name__ == "__main__":
    os.umask(0o077)
    sys.exit(main())
