import hashlib
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("release_approval", ROOT / "scripts" / "validate-release-approval.py")
validator = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validator)
FETCH_SPEC = importlib.util.spec_from_file_location("qualification_fetch", ROOT / "scripts" / "fetch-release-qualification-artifacts.py")
fetch = importlib.util.module_from_spec(FETCH_SPEC); FETCH_SPEC.loader.exec_module(fetch)
DIGEST = "sha256:" + "a" * 64
COMMIT = "b" * 40
TAG = "v1.2.3"
RUN = "123"


class ReleaseApprovalTest(unittest.TestCase):
    def test_source_run_must_be_successful_exact_workflow_commit_and_unique_artifact(self):
        artifact = "cloud-acceptance-123"
        run = {"status": "completed", "conclusion": "success", "event": "workflow_dispatch", "path": ".github/workflows/cloud-acceptance.yaml", "head_sha": COMMIT, "head_branch": TAG, "head_repository": {"full_name": "owner/repo"}}
        artifacts = {"artifacts": [{"name": artifact, "expired": False}]}
        kwargs = {"repository": "owner/repo", "workflow": ".github/workflows/cloud-acceptance.yaml", "commit": COMMIT, "tag": TAG, "artifact": artifact}
        fetch.validate_run_metadata(run, artifacts, **kwargs)
        for field, value in (("conclusion", "failure"), ("path", ".github/workflows/other.yaml"), ("head_sha", "c" * 40), ("head_branch", "main")):
            forged = dict(run); forged[field] = value
            with self.assertRaises(ValueError): fetch.validate_run_metadata(forged, artifacts, **kwargs)
        with self.assertRaisesRegex(ValueError, "unique"):
            fetch.validate_run_metadata(run, {"artifacts": artifacts["artifacts"] * 2}, **kwargs)

    def test_external_descriptor_binds_bytes_run_ref_and_workflow_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            evidence, bundle = root / "cloud.json", root / "bundle.json"
            evidence.write_text('{"result":"passed"}\n', encoding="utf-8")
            bundle.write_text("{}\n", encoding="utf-8")
            descriptor = {
                "status": "passed",
                "evidence_digest": "sha256:" + hashlib.sha256(evidence.read_bytes()).hexdigest(),
                "source_run_id": RUN,
                "source_ref": f"refs/tags/{TAG}",
                "artifact_name": f"cloud-acceptance-{RUN}",
            }
            approval = {"cloud_acceptance": descriptor}
            identities = []
            original = validator.verify_signature
            validator.verify_signature = lambda path, sig, identity: identities.append(identity)
            try:
                self.assertEqual(validator.validate_descriptor(approval, "cloud_acceptance", TAG, evidence, bundle, "owner/repo")["result"], "passed")
                self.assertEqual(identities, [f"https://github.com/owner/repo/.github/workflows/cloud-acceptance.yaml@refs/tags/{TAG}"])
                evidence.write_text('{"result":"failed"}\n', encoding="utf-8")
                with self.assertRaisesRegex(ValueError, "digest"):
                    validator.validate_descriptor(approval, "cloud_acceptance", TAG, evidence, bundle, "owner/repo")
                evidence.write_text('{"result":"passed"}\n', encoding="utf-8")
                approval["cloud_acceptance"]["source_ref"] = "refs/heads/main"
                with self.assertRaisesRegex(ValueError, "exact target tag"):
                    validator.validate_descriptor(approval, "cloud_acceptance", TAG, evidence, bundle, "owner/repo")
            finally:
                validator.verify_signature = original

    def test_cloud_requires_exact_provider_set_and_observed_replay_receipt(self):
        rows = []
        for provider in ("eks", "gke", "aks", "doks"):
            rows.append({
                "provider": provider, "cluster_id_sha256": DIGEST, "credential_id_sha256": DIGEST,
                "original_policy_sha256": DIGEST, "credential_tested": True, "converged": True,
                "idempotent_replay": True, "reconcile_receipt_sha256": DIGEST, "restored": True,
            })
        value = {"schema_version": 1, "result": "passed", "target_version": TAG, "source_commit": COMMIT, "source_run_id": RUN, "base_url_sha256": DIGEST, "test_cidr_sha256": DIGEST, "started_at": "2026-08-25T00:00:00Z", "completed_at": "2026-08-25T00:10:00Z", "targets": rows}
        validator.validate_cloud(value, TAG, COMMIT, RUN)
        value["targets"][0]["reconcile_receipt_sha256"] = ""
        with self.assertRaisesRegex(ValueError, "replay"):
            validator.validate_cloud(value, TAG, COMMIT, RUN)

    def test_accessibility_requires_ios_and_all_critical_workflows(self):
        rows = []
        for platform, browser, technology in validator.ACCESSIBILITY_MATRIX:
            rows.append({
                "platform": platform, "browser": browser, "assistive_technology": technology,
                "at_version": "supported", "viewport": "mobile" if platform == "ios" else "desktop",
                "zoom_levels": ["100%"] if platform == "ios" else ["100%", "200%"],
                "critical_workflows": sorted(validator.CRITICAL_WORKFLOWS), "status": "passed",
                "open_blocking_defects": 0, "evidence_uri": "artifact://matrix", "evidence_sha256": DIGEST,
            })
        value = {"schema_version": 1, "result": "passed", "target_version": TAG, "source_commit": COMMIT, "source_run_id": RUN, "started_at": "2026-08-25T00:00:00Z", "completed_at": "2026-08-25T00:10:00Z", "reviewer": "a11y-owner", "raw_evidence": {"source_run_id": "99", "source_workflow": ".github/workflows/accessibility-raw-evidence.yaml", "source_commit": COMMIT, "source_ref": f"refs/tags/{TAG}", "source_conclusion": "success", "manifest_sha256": DIGEST, "signature_bundle_sha256": DIGEST}, "matrix": rows}
        validator.validate_accessibility(value, TAG, COMMIT, RUN)
        value["matrix"] = [row for row in rows if row["platform"] != "ios"]
        with self.assertRaisesRegex(ValueError, "desktop and iOS"):
            validator.validate_accessibility(value, TAG, COMMIT, RUN)

    def test_rc_inner_validator_rejects_wrong_mode_and_digest(self):
        script = ROOT / "scripts" / "validate-rc-rehearsal-inner.py"
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifest, backup, evidence = root / "release.json", root / "BACKUP_SHA256SUMS", root / "upgrade.json"
            manifest.write_text("{}\n"); backup.write_text("digest  backup\n")
            body = {"schema_version": 1, "release": "astronomer", "namespace": "astronomer", "target_version": TAG, "mode": "dry_run", "result": "passed", "api_identity_sha256": DIGEST, "release_manifest_sha256": "sha256:" + hashlib.sha256(manifest.read_bytes()).hexdigest(), "backup_manifest_sha256": "sha256:" + hashlib.sha256(backup.read_bytes()).hexdigest(), "database_restore_mode": "live_replaced", "rollback_attempted": False, "completed_at": "2026-08-25T00:10:00Z"}
            evidence.write_text(json.dumps(body))
            import subprocess
            command = [str(script), "--upgrade-evidence", str(evidence), "--backup-manifest", str(backup), "--release-manifest", str(manifest), "--target", TAG]
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)
            body["mode"] = "upgrade"; evidence.write_text(json.dumps(body))
            self.assertEqual(subprocess.run(command, capture_output=True).returncode, 0)
            body["database_restore_mode"] = "verified_copy"; evidence.write_text(json.dumps(body))
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)
            body["database_restore_mode"] = "live_replaced"; evidence.write_text(json.dumps(body))
            manifest.write_text('{"tampered":true}\n')
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)


if __name__ == "__main__":
    unittest.main()
