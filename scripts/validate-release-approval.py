#!/usr/bin/env python3
"""Verify a protected approval and every exact, signed qualification artifact."""

import argparse
import datetime as dt
import hashlib
import json
import re
import subprocess
from pathlib import Path

DIGEST = re.compile(r"sha256:[a-f0-9]{64}\Z")
COMMIT = re.compile(r"[a-f0-9]{40}\Z")
TAG = re.compile(r"v1\.[0-9]+\.[0-9]+\Z")
RUN_ID = re.compile(r"[1-9][0-9]*\Z")
PROFILES = {"estate-100", "estate-500", "estate-1000", "estate-1000-soak"}
ACCESSIBILITY_MATRIX = {
    ("windows", "chrome", "nvda"),
    ("windows", "edge", "narrator"),
    ("macos", "safari", "voiceover"),
    ("ios", "safari", "voiceover"),
}
CRITICAL_WORKFLOWS = {
    "bootstrap-login-session-recovery", "adopted-cluster-registration-reconnect",
    "resource-explorer-workload-operations", "rbac-binding-denial", "flux-delivery-lifecycle",
    "backup-restore", "security-scan-findings", "unified-logging", "direct-proxy-access-decommission",
}
EXTERNAL = {
    "rc_rehearsal": ("release-candidate-rehearsal.yaml", "rc-rehearsal-"),
    "cloud_acceptance": ("cloud-acceptance.yaml", "cloud-acceptance-"),
    "scale_certification": ("scale-certification-aggregate.yaml", "scale-certification-set-"),
    "rancher_benchmark": ("rancher-benchmark.yaml", "rancher-benchmark-"),
    "accessibility": ("accessibility-acceptance.yaml", "accessibility-acceptance-"),
}


def closed(value, keys, label):
    if not isinstance(value, dict) or set(value) != set(keys):
        raise ValueError(f"{label} violates closed schema")


def regular(path: Path, label: str) -> None:
    if not path.is_file() or path.is_symlink():
        raise ValueError(f"{label} must be a regular non-symlink file")


def read_json(path: Path, label: str) -> dict:
    regular(path, label)
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{label} must contain an object")
    return value


def sha256(path: Path) -> str:
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def stamp(value: str, label: str) -> dt.datetime:
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise ValueError(f"{label} requires timezone")
    return parsed


def verify_signature(path: Path, bundle: Path, identity: str) -> None:
    regular(bundle, "signature bundle")
    subprocess.run(
        ["cosign", "verify-blob", "--bundle", str(bundle), "--certificate-identity", identity,
         "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com", str(path)],
        check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )


def validate_descriptor(approval: dict, name: str, tag: str, path: Path, bundle: Path, repository: str) -> dict:
    descriptor = approval[name]
    closed(descriptor, {"status", "evidence_digest", "source_run_id", "source_ref", "artifact_name"}, name)
    if descriptor["status"] != "passed" or not DIGEST.fullmatch(descriptor["evidence_digest"]):
        raise ValueError(f"{name} is not approved with an exact digest")
    if not RUN_ID.fullmatch(descriptor["source_run_id"]):
        raise ValueError(f"{name} source run is invalid")
    expected_ref = f"refs/tags/{tag}"
    if descriptor["source_ref"] != expected_ref:
        raise ValueError(f"{name} source ref is not the exact target tag")
    workflow, artifact_prefix = EXTERNAL[name]
    if descriptor["artifact_name"] != f"{artifact_prefix}{descriptor['source_run_id']}":
        raise ValueError(f"{name} artifact name does not bind its source run")
    regular(path, name)
    if sha256(path) != descriptor["evidence_digest"]:
        raise ValueError(f"{name} digest does not bind downloaded artifact")
    verify_signature(path, bundle, f"https://github.com/{repository}/.github/workflows/{workflow}@{expected_ref}")
    return read_json(path, name)


def validate_rc(value: dict, tag: str, commit: str, release_run_id: str, producer_run_id: str) -> None:
    closed(value, {"schema_version", "result", "target_version", "previous_version", "source_run_id", "producer_run_id", "source_commit", "release_manifest_sha256", "upgrade_evidence_sha256", "backup_manifest_sha256", "backup_restore", "decrypt_proof", "clean_restore", "destructive_fence", "started_at", "completed_at"}, "RC evidence")
    if value["schema_version"] != 1 or value["result"] != "passed" or value["target_version"] != tag or value["source_commit"] != commit or value["source_run_id"] != release_run_id or value["producer_run_id"] != producer_run_id:
        raise ValueError("RC evidence does not bind the exact release")
    if value["previous_version"] == tag or not TAG.fullmatch(value["previous_version"]):
        raise ValueError("RC evidence previous version is invalid")
    for field in ("release_manifest_sha256", "upgrade_evidence_sha256", "backup_manifest_sha256"):
        if not DIGEST.fullmatch(value[field]): raise ValueError(f"RC evidence {field} is invalid")
    if (value["backup_restore"], value["decrypt_proof"], value["clean_restore"], value["destructive_fence"]) != ("passed", "passed", "passed", "owned_disposable_k3d"):
        raise ValueError("RC evidence qualification flags are not passed")


def validate_cloud(value: dict, tag: str, commit: str, run_id: str) -> None:
    closed(value, {"schema_version", "result", "target_version", "source_commit", "source_run_id", "base_url_sha256", "test_cidr_sha256", "started_at", "completed_at", "targets"}, "cloud evidence")
    if value["schema_version"] != 1 or value["result"] != "passed" or value["target_version"] != tag or value["source_commit"] != commit or value["source_run_id"] != run_id:
        raise ValueError("cloud evidence does not bind the exact release")
    targets = value["targets"]
    if not isinstance(targets, list) or {row.get("provider") for row in targets if isinstance(row, dict)} != {"eks", "gke", "aks", "doks"} or len(targets) != 4:
        raise ValueError("cloud evidence does not contain exactly four providers")
    for row in targets:
        closed(row, {"provider", "cluster_id_sha256", "credential_id_sha256", "original_policy_sha256", "credential_tested", "converged", "idempotent_replay", "reconcile_receipt_sha256", "restored"}, "cloud target")
        if any(row[field] is not True for field in ("credential_tested", "converged", "idempotent_replay", "restored")) or not DIGEST.fullmatch(row["reconcile_receipt_sha256"]):
            raise ValueError("cloud target did not pass independently observed replay and restoration")


def validate_scale(value: dict, tag: str, commit: str, run_id: str, release_manifest_sha256: str) -> None:
    closed(value, {"schema_version", "target_version", "source_commit", "source_run_id", "completed_at", "release", "profiles", "component_sizing"}, "scale evidence")
    if value["schema_version"] != "astronomer-scale-certification-set-v1" or value["target_version"] != tag or value["source_commit"] != commit or value["source_run_id"] != run_id or value.get("release", {}).get("commit") != commit or value.get("release", {}).get("images") != release_manifest_sha256:
        raise ValueError("scale evidence does not bind the release commit")
    profiles = value["profiles"]
    if not isinstance(profiles, list) or len(profiles) != 4 or {row.get("profile") for row in profiles if isinstance(row, dict)} != PROFILES:
        raise ValueError("scale evidence does not contain the four production profiles")
    for row in profiles:
        closed(row, {"profile", "manifest_sha256", "signature_bundle_sha256", "baseline"}, "scale profile")
        baseline = row["baseline"]
        if baseline.get("verdict") != "pass" or baseline.get("audit_accepted", 0) <= 0 or baseline.get("audit_accepted") != baseline.get("audit_canonical_rows") or baseline.get("audit_duplicates") != 0 or baseline.get("audit_lost") != 0:
            raise ValueError("scale profile lacks passing retained mandatory-audit evidence")
    sizing = value["component_sizing"]
    closed(sizing, {"recommendations_sha256", "docs_table_sha256", "status"}, "component sizing")
    if sizing["status"] != "evidence-derived-unpublished" or any(not re.fullmatch(r"[a-f0-9]{64}", sizing[field]) for field in ("recommendations_sha256", "docs_table_sha256")):
        raise ValueError("scale component sizing is not retained measured evidence")


def validate_rancher(value: dict, commit: str, run_id: str, path: Path) -> None:
    if value.get("schema_version") != "rancher-ux-benchmark/v1" or value.get("qualification") != {"automated": "pass", "human_clarity": "pass", "overall": "pass"} or value.get("subjects", {}).get("astronomer", {}).get("source_commit") != commit or value.get("run", {}).get("run_id") != run_id:
        raise ValueError("Rancher benchmark does not bind a fully passed release comparison")
    subprocess.run(["node", str(Path(__file__).with_name("validate-rancher-benchmark.mjs")), "--evidence", str(path)], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def validate_accessibility(value: dict, tag: str, commit: str, run_id: str) -> None:
    closed(value, {"schema_version", "result", "target_version", "source_commit", "source_run_id", "started_at", "completed_at", "reviewer", "raw_evidence", "matrix"}, "accessibility evidence")
    if value["schema_version"] != 1 or value["result"] != "passed" or value["target_version"] != tag or value["source_commit"] != commit or value["source_run_id"] != run_id:
        raise ValueError("accessibility evidence does not bind the exact release")
    matrix = value["matrix"]
    raw = value["raw_evidence"]
    closed(raw, {"source_run_id", "source_workflow", "source_commit", "source_ref", "source_conclusion", "manifest_sha256", "signature_bundle_sha256"}, "accessibility raw evidence")
    if raw["source_workflow"] != ".github/workflows/accessibility-raw-evidence.yaml" or raw["source_commit"] != commit or raw["source_ref"] != f"refs/tags/{tag}" or raw["source_conclusion"] != "success" or not RUN_ID.fullmatch(raw["source_run_id"]) or not DIGEST.fullmatch(raw["manifest_sha256"]) or not DIGEST.fullmatch(raw["signature_bundle_sha256"]):
        raise ValueError("accessibility raw evidence provenance is invalid")
    if not isinstance(matrix, list) or len(matrix) != 4 or {(row.get("platform"), row.get("browser"), row.get("assistive_technology")) for row in matrix if isinstance(row, dict)} != ACCESSIBILITY_MATRIX:
        raise ValueError("accessibility evidence does not contain the exact desktop and iOS matrix")
    for row in matrix:
        closed(row, {"platform", "browser", "assistive_technology", "at_version", "viewport", "zoom_levels", "critical_workflows", "status", "open_blocking_defects", "evidence_uri", "evidence_sha256"}, "accessibility matrix row")
        if row["status"] != "passed" or row["open_blocking_defects"] != 0 or set(row["critical_workflows"]) != CRITICAL_WORKFLOWS or not DIGEST.fullmatch(row["evidence_sha256"]):
            raise ValueError("accessibility matrix row is incomplete or unpassed")
        if row["platform"] != "ios" and set(row["zoom_levels"]) != {"100%", "200%"}:
            raise ValueError("desktop accessibility row lacks both required zoom levels")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("path", type=Path)
    parser.add_argument("--tag", required=True); parser.add_argument("--source-commit", required=True); parser.add_argument("--source-run-id", required=True); parser.add_argument("--repository", required=True)
    for name in ("runtime-image", "rc", "cloud", "scale", "rancher", "accessibility"):
        parser.add_argument(f"--{name}-evidence", type=Path, required=True)
        parser.add_argument(f"--{name}-bundle", type=Path, required=True)
    args = parser.parse_args()
    approval = read_json(args.path, "approval")
    closed(approval, {"schema_version", "tag", "source_commit", "source_run_id", "approver", "approved_at", "runtime_images", *EXTERNAL}, "approval")
    if approval["schema_version"] != 2 or approval["tag"] != args.tag or approval["source_commit"] != args.source_commit or approval["source_run_id"] != args.source_run_id or not TAG.fullmatch(args.tag) or not COMMIT.fullmatch(args.source_commit) or not RUN_ID.fullmatch(args.source_run_id):
        raise ValueError("approval does not bind the exact promotion")
    approved_at = stamp(approval["approved_at"], "approval timestamp")
    if not isinstance(approval["approver"], str) or not approval["approver"].strip(): raise ValueError("named approver is required")
    runtime = approval["runtime_images"]
    closed(runtime, {"status", "evidence_digest"}, "runtime image qualification")
    regular(args.runtime_image_evidence, "runtime image evidence")
    if runtime["status"] != "passed" or sha256(args.runtime_image_evidence) != runtime["evidence_digest"]: raise ValueError("runtime image evidence digest mismatch")
    verify_signature(args.runtime_image_evidence, args.runtime_image_bundle, f"https://github.com/{args.repository}/.github/workflows/release.yaml@refs/tags/{args.tag}")
    runtime_value = read_json(args.runtime_image_evidence, "runtime image evidence")
    if runtime_value.get("result") != "passed" or runtime_value.get("release_version") != args.tag: raise ValueError("runtime image evidence does not bind release")
    artifacts = {
        "rc_rehearsal": (args.rc_evidence, args.rc_bundle), "cloud_acceptance": (args.cloud_evidence, args.cloud_bundle),
        "scale_certification": (args.scale_evidence, args.scale_bundle), "rancher_benchmark": (args.rancher_evidence, args.rancher_bundle),
        "accessibility": (args.accessibility_evidence, args.accessibility_bundle),
    }
    values = {name: validate_descriptor(approval, name, args.tag, *paths, args.repository) for name, paths in artifacts.items()}
    validate_rc(values["rc_rehearsal"], args.tag, args.source_commit, args.source_run_id, approval["rc_rehearsal"]["source_run_id"])
    validate_cloud(values["cloud_acceptance"], args.tag, args.source_commit, approval["cloud_acceptance"]["source_run_id"])
    validate_scale(values["scale_certification"], args.tag, args.source_commit, approval["scale_certification"]["source_run_id"], runtime_value["release_manifest_sha256"])
    validate_rancher(values["rancher_benchmark"], args.source_commit, approval["rancher_benchmark"]["source_run_id"], args.rancher_evidence)
    validate_accessibility(values["accessibility"], args.tag, args.source_commit, approval["accessibility"]["source_run_id"])
    for label, value in values.items():
        completed = stamp(value.get("completed_at", value.get("run", {}).get("finished_at", "")), f"{label} completion")
        if completed > approved_at + dt.timedelta(minutes=5) or completed < approved_at - dt.timedelta(days=30): raise ValueError(f"{label} evidence is future or stale relative to approval")


if __name__ == "__main__":
    main()
