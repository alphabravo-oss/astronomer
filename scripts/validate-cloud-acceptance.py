#!/usr/bin/env python3
"""Fail-safe live acceptance for four explicitly adopted cloud clusters.

This driver never creates clusters or credentials. It tests a pre-existing,
explicitly linked credential, snapshots the current allow-list, adds one public
host CIDR, proves durable reconcile/idempotent replay, and restores the exact
policy in a finally block. Only digests and booleans enter the public report.
"""

from __future__ import annotations

import datetime as dt
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

PROVIDERS = {"eks", "gke", "aks", "doks"}

def die(message: str) -> "NoReturn":
    raise RuntimeError(message)

def digest(value: str | bytes) -> str:
    raw = value if isinstance(value, bytes) else value.encode()
    return "sha256:" + hashlib.sha256(raw).hexdigest()

def canonical(value: object) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode()

def now() -> str:
    return dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")

class API:
    def __init__(self, base: str, token: str, timeout: int):
        self.base, self.timeout = base.rstrip("/"), timeout
        self.headers = {"Authorization": "Bearer " + token, "Accept": "application/json"}

    def call(self, method: str, path: str, body: object | None = None, idempotency: str = "") -> object:
        headers = dict(self.headers)
        payload = None
        if body is not None:
            payload, headers["Content-Type"] = canonical(body), "application/json"
        if idempotency:
            headers["Idempotency-Key"] = idempotency
        req = urllib.request.Request(self.base + path, data=payload, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout, context=ssl.create_default_context()) as response:
                return json.loads(response.read(1 << 20))
        except urllib.error.HTTPError as exc:
            # Never surface response bodies; providers can place account, role,
            # network or credential details in error text.
            raise RuntimeError(f"API {method} {path.split('?')[0]} returned HTTP {exc.code}") from None

def data(value: object) -> dict:
    if not isinstance(value, dict): die("API response is not an object")
    result = value.get("data", value)
    if not isinstance(result, dict): die("API data is not an object")
    return result

def policy_view(value: dict) -> dict:
    cidrs = value.get("operator_cidrs", [])
    mode = value.get("mode", "monitor")
    if not isinstance(cidrs, list) or not all(isinstance(item, str) for item in cidrs): die("invalid allow-list response")
    if mode not in {"monitor", "enforce"}: die("invalid allow-list mode")
    return {"cidrs": sorted(set(cidrs)), "mode": mode}

def validate_replay_receipts(first: object, second: object, cluster_id: str) -> dict:
    first_receipt, second_receipt = data(first), data(second)
    expected = {"cluster_id": cluster_id, "status": "accepted"}
    if first_receipt != expected or second_receipt != first_receipt:
        die("idempotent replay did not return the exact durable reconcile receipt")
    return first_receipt

def poll(api: API, cluster_id: str, predicate, deadline: float) -> dict:
    path = f"/api/v1/clusters/{cluster_id}/apiserver-allowlist/"
    while time.monotonic() < deadline:
        current = data(api.call("GET", path))
        if predicate(current): return current
        time.sleep(2)
    die("bounded convergence deadline exceeded")

def main() -> int:
    base = os.environ.get("ASTRO_BASE_URL", "").rstrip("/")
    expected = os.environ.get("ASTRO_EXPECTED_BASE_URL", "").rstrip("/")
    token = os.environ.get("ASTRO_AUTH_TOKEN", "")
    targets_path = Path(os.environ.get("CLOUD_ACCEPTANCE_TARGETS", ""))
    cidr_text = os.environ.get("CLOUD_ACCEPTANCE_CIDR", "")
    evidence_dir = Path(os.environ.get("CLOUD_ACCEPTANCE_ARTIFACT_DIR", "cloud-acceptance-evidence"))
    timeout = int(os.environ.get("CLOUD_ACCEPTANCE_TIMEOUT_SECONDS", "300"))
    target_version = os.environ.get("CLOUD_ACCEPTANCE_TARGET_VERSION", "")
    source_commit = os.environ.get("CLOUD_ACCEPTANCE_SOURCE_COMMIT", "")
    source_run_id = os.environ.get("CLOUD_ACCEPTANCE_SOURCE_RUN_ID", "")
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment: die("ASTRO_BASE_URL must be a credential-free HTTPS origin")
    if base != expected: die("management API identity does not match ASTRO_EXPECTED_BASE_URL")
    if not token or any(ch.isspace() for ch in token): die("ASTRO_AUTH_TOKEN is required and malformed")
    if timeout < 30 or timeout > 900: die("timeout must be between 30 and 900 seconds")
    if not re.fullmatch(r"v1\.[0-9]+\.[0-9]+", target_version): die("CLOUD_ACCEPTANCE_TARGET_VERSION must be an exact v1 tag")
    if not re.fullmatch(r"[a-f0-9]{40}", source_commit): die("CLOUD_ACCEPTANCE_SOURCE_COMMIT must be an exact commit")
    if not re.fullmatch(r"[1-9][0-9]*", source_run_id): die("CLOUD_ACCEPTANCE_SOURCE_RUN_ID must be an exact run ID")
    network = ipaddress.ip_network(cidr_text, strict=True)
    if network.prefixlen != network.max_prefixlen or not network.network_address.is_global: die("CLOUD_ACCEPTANCE_CIDR must be one globally routable /32 or /128 host")
    if not targets_path.is_file() or targets_path.is_symlink(): die("CLOUD_ACCEPTANCE_TARGETS must be a regular file")
    config = json.loads(targets_path.read_text())
    if set(config) != {"schema_version", "targets"} or config["schema_version"] != 1 or not isinstance(config["targets"], list): die("targets document violates closed schema")
    targets = config["targets"]
    if len(targets) != 4 or {item.get("provider") for item in targets if isinstance(item, dict)} != PROVIDERS: die("exactly one EKS, GKE, AKS and DOKS target is required")
    allowed_keys = {"provider", "cluster_id", "cluster_name", "project_id", "credential_id"}
    for item in targets:
        if set(item) != allowed_keys: die("target contains missing or unknown properties")
        uuid.UUID(item["cluster_id"]); uuid.UUID(item["project_id"]); uuid.UUID(item["credential_id"])
        if not item["cluster_name"] or len(item["cluster_name"]) > 253: die("target cluster_name is invalid")

    api, started = API(base, token, 15), now()
    private = evidence_dir / ".recovery"
    private.mkdir(parents=True, exist_ok=True, mode=0o700); os.chmod(private, 0o700)
    results, snapshots, passed = [], {}, False
    try:
        for item in targets:
            provider, cluster_id, project_id, credential_id = item["provider"], item["cluster_id"], item["project_id"], item["credential_id"]
            cluster = data(api.call("GET", f"/api/v1/clusters/{cluster_id}/"))
            if cluster.get("id") != cluster_id or cluster.get("name") != item["cluster_name"]: die("cluster identity differs from explicit adopted target")
            observed_provider = str(cluster.get("provider", "")).lower()
            if observed_provider not in {provider, {"eks":"aws", "gke":"gcp", "aks":"azure", "doks":"digitalocean"}[provider]}: die("cluster provider differs from explicit target")
            credential_path = f"/api/v1/projects/{project_id}/cloud-credentials/{credential_id}/"
            credential = data(api.call("GET", credential_path))
            expected_credential_provider = {"eks":"aws", "gke":"gcp", "aks":"azure", "doks":"digitalocean"}[provider]
            if credential.get("id") != credential_id or credential.get("project_id") != project_id or credential.get("provider") != expected_credential_provider: die("credential identity/provider differs from explicit target")
            if cluster_id not in {str(ref.get("cluster_id")) for ref in credential.get("target_refs", []) if isinstance(ref, dict)}: die("credential is not explicitly linked to adopted cluster")
            tested = data(api.call("POST", credential_path + "test/"))
            if tested.get("ok") is not True: die("linked cloud credential test failed")
            path = f"/api/v1/clusters/{cluster_id}/apiserver-allowlist/"
            original_response = data(api.call("GET", path))
            original = {"policy": policy_view(original_response), "effective": sorted(set(original_response.get("effective", [])))}
            if cidr_text in original["policy"]["cidrs"] or cidr_text in original["effective"]: die("test CIDR already exists; a no-op cannot prove provider convergence")
            snapshots[cluster_id] = original
            snapshot_path = private / f"{digest(cluster_id)[7:]}.json"
            snapshot_path.write_bytes(canonical(original)); os.chmod(snapshot_path, 0o600)
            desired = {"cidrs": sorted(set(original["policy"]["cidrs"] + [cidr_text])), "mode": "enforce", "force_apply": True}
            api.call("PUT", path, desired)
            replay_key = str(uuid.uuid4())
            reconcile_path = path + "reconcile/"
            first_response = api.call("POST", reconcile_path, idempotency=replay_key)
            second_response = api.call("POST", reconcile_path, idempotency=replay_key)
            first_receipt = validate_replay_receipts(first_response, second_response, cluster_id)
            poll(api, cluster_id, lambda current: cidr_text in current.get("effective", []) and current.get("sync_status") == "synced" and not current.get("drift", True), time.monotonic() + timeout)
            results.append({"provider": provider, "cluster_id_sha256": digest(cluster_id), "credential_id_sha256": digest(credential_id), "original_policy_sha256": digest(canonical(original)), "credential_tested": True, "converged": True, "idempotent_replay": True, "reconcile_receipt_sha256": digest(canonical(first_receipt)), "restored": False})
        passed = True
    finally:
        restore_failed = False
        for item in reversed(targets):
            cluster_id = item["cluster_id"]
            if cluster_id not in snapshots: continue
            try:
                original, path = snapshots[cluster_id], f"/api/v1/clusters/{cluster_id}/apiserver-allowlist/"
                api.call("PUT", path, {**original["policy"], "force_apply": True})
                api.call("POST", path + "reconcile/", idempotency=str(uuid.uuid4()))
                poll(api, cluster_id, lambda current, original=original: policy_view(current) == original["policy"] and sorted(set(current.get("effective", []))) == original["effective"], time.monotonic() + timeout)
                for result in results:
                    if result["cluster_id_sha256"] == digest(cluster_id): result["restored"] = True
            except Exception:
                restore_failed = True
        passed = passed and not restore_failed and len(results) == 4 and all(item["restored"] for item in results)
        evidence_dir.mkdir(parents=True, exist_ok=True); os.chmod(evidence_dir, 0o755)
        report = {"schema_version": 1, "result": "passed" if passed else "failed", "target_version": target_version, "source_commit": source_commit, "source_run_id": source_run_id, "base_url_sha256": digest(base), "test_cidr_sha256": digest(cidr_text), "started_at": started, "completed_at": now(), "targets": results}
        output = evidence_dir / "cloud-acceptance.json"
        output.write_bytes(canonical(report)); os.chmod(output, 0o644)
    if not passed: die("cloud acceptance failed or exact restoration could not be verified")
    print(str(evidence_dir / "cloud-acceptance.json"))
    return 0

if __name__ == "__main__":
    try: raise SystemExit(main())
    except Exception as exc:
        print(f"cloud-acceptance: {exc}", file=sys.stderr)
        raise SystemExit(1)
