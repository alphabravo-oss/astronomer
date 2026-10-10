#!/usr/bin/env python3
"""Create only one fenced, absent metrics DB from its closed owned template."""
import argparse
import datetime as dt
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
from urllib.parse import parse_qs, urlsplit, urlunsplit

SPEC = importlib.util.spec_from_file_location("metrics", Path(__file__).with_name("metrics-postgres-qualification.py"))
metrics = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(metrics)


def template_reference(path, major):
    path = Path(path)
    info = path.lstat()
    if path.is_symlink() or not path.is_file() or info.st_mode & 0o077:
        raise ValueError("private template reference required")
    raw = path.read_bytes()
    doc = json.loads(raw)
    if set(doc) != {"database_url"}:
        raise ValueError("template reference schema differs")
    address = urlsplit(doc["database_url"])
    template = "plan032_w07_cursor" + major
    if address.scheme not in {"postgres", "postgresql"} or not address.hostname or not address.port or address.path != "/" + template or address.fragment:
        raise ValueError("exclusive template endpoint differs")
    query = parse_qs(address.query, keep_blank_values=True, strict_parsing=True)
    if any(key not in {"sslmode", "sslrootcert", "connect_timeout", "application_name"} or len(values) != 1 for key, values in query.items()):
        raise ValueError("template connection override rejected")
    return address, template, hashlib.sha256(raw).hexdigest()


def sql(args, primary, database, statement):
    cmd = [args.kubectl, "--kubeconfig", args.kubeconfig, "--request-timeout=10s", "-n", metrics.NAMESPACE, "exec", "-i", primary, "--", "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", database]
    result = subprocess.run(cmd, input="SET statement_timeout='10s'; SET lock_timeout='5s';\n" + statement, capture_output=True, text=True, timeout=30)
    if result.returncode:
        # Kubernetes and SQL diagnostics are not safe public evidence.
        raise ValueError("owned SQL operation failed")
    return result.stdout.strip()


def inspect(args, address, template):
    lane = metrics.LANES[args.major]
    report = metrics.owned_endpoint(args, address, lane)
    primary = report["currentPrimary"]
    state = sql(args, primary, template, "SELECT current_database(), current_setting('server_version_num')::int / 10000, version, dirty FROM public.schema_migrations ORDER BY version DESC LIMIT 1;\n")
    if state != template + "|" + args.major + "|75|f":
        raise ValueError("actual template major or clean schema75 differs")
    destination = lane["database"]
    fence = sql(args, primary, "postgres", "SELECT (SELECT count(*) FROM pg_database WHERE datname='" + destination + "'), (SELECT count(*) FROM pg_stat_activity WHERE datname='" + template + "'), (SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='" + template + "');\n")
    if fence != "0|0|astronomer":
        raise ValueError("destination exists, template active, or template owner differs")
    return report


def allocate(args):
    artifact = Path(args.artifact_dir).resolve()
    artifact.mkdir(mode=0o700, exist_ok=False)
    destination_path = Path(args.reference).resolve()
    receipt = {"startedAt": dt.datetime.now(dt.timezone.utc).isoformat(), "major": args.major, "created": False, "exitCode": 1, "templateRestoreQualified": False}
    try:
        if destination_path.exists() or destination_path.is_symlink():
            raise ValueError("reference already exists")
        destination_path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        address, template, digest = template_reference(args.template_reference, args.major)
        receipt.update(templateDatabase=template, templateReferenceSHA256=digest)
        capacity = subprocess.run(["bash", "scripts/check-build-capacity.sh", "--json"], cwd=metrics.ROOT, capture_output=True, text=True)
        (artifact / "capacity-before.json").write_text(capacity.stdout)
        if capacity.returncode:
            raise ValueError("capacity guard failed")
        # Immediately before CREATE, repeat every live endpoint/UID/DB fence.
        report = inspect(args, address, template)
        receipt["ownershipBeforeCreate"] = report
        if not args.create:
            receipt["exitCode"] = 0
            return 0
        database = metrics.LANES[args.major]["database"]
        sql(args, report["currentPrimary"], "postgres", 'CREATE DATABASE "' + database + '" TEMPLATE "' + template + '" OWNER astronomer;\n')
        receipt["created"] = True
        # Never drop or reuse a destination if comment/reference creation fails.
        sql(args, report["currentPrimary"], "postgres", 'COMMENT ON DATABASE "' + database + '" IS \'plan032-20261009 exclusive W10 metric snapshot fixture\';\n')
        new_url = urlunsplit(address._replace(path="/" + database))
        reference = {"database_url": new_url, "database": database, "cluster_uid": metrics.LANES[args.major]["uid"], "primary_at_allocation": report["currentPrimary"]}
        with destination_path.open("x") as handle:
            handle.write(json.dumps(reference) + "\n")
        destination_path.chmod(0o600)
        receipt.update(privateReferencePath=str(destination_path), privateReferenceSHA256=hashlib.sha256(destination_path.read_bytes()).hexdigest(), exitCode=0)
        return 0
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError):
        receipt["errorKind"] = "ownership_or_allocation_failure"
        return 1
    finally:
        receipt["completedAt"] = dt.datetime.now(dt.timezone.utc).isoformat()
        (artifact / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
        print(json.dumps({key: receipt[key] for key in ["major", "created", "exitCode"]}))


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--major", choices=metrics.LANES, required=True)
    parser.add_argument("--template-reference", required=True)
    parser.add_argument("--reference", required=True)
    parser.add_argument("--kubeconfig", required=True)
    parser.add_argument("--kubectl", required=True)
    parser.add_argument("--artifact-dir", required=True)
    parser.add_argument("--create", action="store_true", help="otherwise inspect only; no database mutation")
    return allocate(parser.parse_args())


if __name__ == "__main__":
    sys.exit(main())
