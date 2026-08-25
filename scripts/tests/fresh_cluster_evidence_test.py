#!/usr/bin/env python3
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

root = Path(__file__).resolve().parents[2]
writer = root / "scripts" / "write-fresh-cluster-evidence.py"
with tempfile.TemporaryDirectory() as directory:
    output = Path(directory) / "nested" / "evidence.json"
    environment = dict(os.environ)
    environment["ASTRO_PASSWORD"] = "must-not-appear"
    subprocess.run([
        sys.executable, str(writer), "--output", str(output), "--status", "pass", "--exit-code", "0",
        "--started-at", "2026-08-24T00:00:00Z", "--completed-at", "2026-08-24T00:01:00Z",
        "--commit", "abc123", "--workflow", "smoke", "--run-id", "42", "--run-attempt", "1",
        "--job", "smoke", "--repository", "example/astronomer", "--ref", "refs/heads/main",
        "--cluster-name", "smoke-42", "--cluster-id", "cluster-id", "--kubernetes-version", "v1.35.0",
        "--flux-version", "v2.9.3", "--agent-image", "agent@sha256:a", "--shell-image", "shell@sha256:b",
        "--k3s-image", "k3s@sha256:c", "--flux-image", "source@sha256:d",
        "--management-image", "server@sha256:e", "--check", "agent_connected",
        "--skipped-check", "vulnerability_reports_not_in_default_baseline",
    ], check=True, env=environment)
    document = json.loads(output.read_text(encoding="utf-8"))
    assert document["schema_version"] == "astronomer-fresh-cluster-smoke/v1"
    assert document["status"] == "pass" and document["exit_code"] == 0
    assert document["source"]["commit"] == "abc123"
    assert document["run"]["run_id"] == "42"
    assert document["checks"] == [
        {"id": "agent_connected", "status": "pass"},
        {"id": "vulnerability_reports_not_in_default_baseline", "status": "skip"},
    ]
    assert document["images"]["management_plane"] == ["server@sha256:e"]
    assert "must-not-appear" not in output.read_text(encoding="utf-8")

workflow = (root / ".github" / "workflows" / "smoke-fresh-cluster.yaml").read_text(encoding="utf-8")
assert "SMOKE_EVIDENCE_FILE: smoke-evidence/evidence.json" in workflow
assert "if: ${{ always() }}" in workflow
assert "path: smoke-evidence/evidence.json" in workflow
assert "retention-days: 90" in workflow

smoke = (root / "scripts" / "smoke-fresh-cluster.sh").read_text(encoding="utf-8")
assert 'write_evidence "$rc"' in smoke
assert smoke.index('write_evidence "$rc"') < smoke.index('k3d cluster delete "$SMOKE_CLUSTER"')
assert 'deploy/bundles/catalog.json' in smoke
assert 'item["default_enabled"]' in smoke
assert 'spec.get("targetNamespace", "")' in smoke
assert 'spec.get("releaseName", "")' in smoke
assert '/tools/status/' not in smoke
assert 'expected_tools=' not in smoke
assert 'ready_builtin_releases" -ge' not in smoke
for legacy_slug in ("fluent-bit", "cert-manager", "ingress-nginx", "gatekeeper"):
    assert legacy_slug not in smoke
assert "ASTRO_PASSWORD" not in writer.read_text(encoding="utf-8")

print("fresh-cluster evidence writer tests passed")
