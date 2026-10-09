#!/usr/bin/env python3
"""Prepare or verify an offline signed resolution bound to selected authority."""
import argparse
import datetime as dt
import hashlib
import importlib.util
import json
import pathlib
import subprocess

ROOT = pathlib.Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("toolchain", ROOT / "scripts/toolchain.py")
toolchain = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(toolchain)
IDENTITY = r"^https://github\.com/alphabravo-oss/astronomer/\.github/workflows/(toolchain-resolution\.yaml@refs/heads/main|release\.yaml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+)$"


def bindings(document):
    return [{"id": c["id"], "selected": c["selected"], "upstreamStable": c["discovery"]["version"], "observedAt": c["discovery"]["observedAt"], "artifactDigests": [a["digest"] for a in c["artifacts"]]} for c in document["components"]]


def verify_binding(receipt, document, authority, now=None):
    now = now or dt.datetime.now(dt.timezone.utc)
    if receipt.get("schemaVersion") != 1:
        raise ValueError("unsupported resolution schema")
    if toolchain.timestamp(receipt["expiresAt"]) <= toolchain.timestamp(receipt["generatedAt"]):
        raise ValueError("signed resolution expiry precedes generation")
    if receipt["authorityDigest"] != "sha256:" + hashlib.sha256(authority).hexdigest():
        raise ValueError("signed resolution authority digest mismatch")
    if receipt["components"] != bindings(document):
        raise ValueError("signed resolution component/artifact bindings differ")
    if toolchain.timestamp(receipt["generatedAt"]) > now or toolchain.timestamp(receipt["expiresAt"]) <= now:
        raise ValueError("signed resolution is future or expired")
    if toolchain.timestamp(receipt["expiresAt"]) - toolchain.timestamp(receipt["generatedAt"]) > dt.timedelta(days=document["policy"]["maxDiscoveryAgeDays"]):
        raise ValueError("signed resolution lifetime exceeds freshness policy")
    toolchain.validate(document, now=now, release=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--generate", action="store_true")
    mode.add_argument("--verify", action="store_true")
    parser.add_argument("--record", type=pathlib.Path, required=True)
    parser.add_argument("--bundle", type=pathlib.Path)
    args = parser.parse_args()
    subprocess.run(["python3", str(ROOT / "scripts/toolchain.py"), "--check"], check=True)
    authority = toolchain.AUTHORITY.read_bytes()
    document = toolchain.validate(json.loads(authority), release=True)
    if args.generate:
        now = dt.datetime.now(dt.timezone.utc)
        receipt = {"schemaVersion": 1, "authorityDigest": "sha256:" + hashlib.sha256(authority).hexdigest(), "generatedAt": now.isoformat(), "expiresAt": (now + dt.timedelta(days=document["policy"]["maxDiscoveryAgeDays"])).isoformat(), "sourceCommit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(), "components": bindings(document)}
        args.record.parent.mkdir(parents=True, exist_ok=True)
        args.record.write_text(toolchain.serialized(receipt))
        print("Prepared unsigned resolution; release acceptance requires an authenticated bundle")
        return
    if not args.bundle:
        raise ValueError("release requires a signed Sigstore resolution bundle")
    subprocess.run(["cosign", "verify-blob", "--offline", "--bundle", str(args.bundle), "--certificate-identity-regexp", IDENTITY, "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com", str(args.record)], check=True)
    subprocess.run(["go", "run", "./deploy/toolchain/check", "deploy/toolchain/resolution.schema.json", str(args.record.resolve())], cwd=ROOT, check=True)
    receipt = json.loads(args.record.read_text())
    verify_binding(receipt, document, authority)
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    if receipt["sourceCommit"] != commit:
        raise ValueError("signed resolution belongs to a different source commit")
    print("Offline signed resolution verified against exact source, authority and artifacts")


if __name__ == "__main__":
    main()
