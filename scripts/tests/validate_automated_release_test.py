import copy
import datetime as dt
import hashlib
import importlib.util
import io
import json
import subprocess
import tempfile
import tarfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("automated", ROOT / "scripts/validate-automated-release.py")
validator = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validator)


class AutomatedReleaseTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.args = SimpleNamespace(tag="v1.2.0", source_commit="a" * 40, source_run_id="123", producer_run_id="456", repository="owner/repo", rc_identity="https://github.com/owner/repo/.github/workflows/resume-release.yaml@refs/heads/main")
        self.write("release-manifest", {"release": {"version": self.args.tag, "source_commit": self.args.source_commit}})
        digest = validator.approval.sha256(self.root / "release-manifest.json")
        self.runtime = {"result": "passed", "vulnerability_qualification": "passed", "license_qualification": "deferred", "release_version": self.args.tag, "release_manifest_sha256": digest}
        self.waiver = dict(reference="example.com/image@sha256:" + "a" * 64, category="vulnerability", ids=["CVE-2026-1234"], reason="Upstream pending", approved_by="Release owner", expires_at="2026-11-06T00:00:00Z", release_version="v1.2.0")
        self.retain_waivers()
        self.rc = dict(schema_version=1, result="passed", target_version=self.args.tag, previous_version="v1.1.0", source_commit=self.args.source_commit, source_run_id="123", producer_run_id="456", release_manifest_sha256=digest, upgrade_evidence_sha256=digest, backup_manifest_sha256=digest, backup_restore="passed", decrypt_proof="passed", clean_restore="passed", destructive_fence="owned_disposable_k3d", started_at="2026-10-07T12:00:00Z", completed_at="2026-10-07T12:10:00Z")
        self.write("runtime-image-evidence", self.runtime)
        self.write("rc-rehearsal-evidence", self.rc)
        mock = patch.object(validator.approval, "verify_signature")
        self.verify = mock.start()
        self.addCleanup(mock.stop)

    def write(self, name, value):
        (self.root / f"{name}.json").write_text(json.dumps(value))

    def retain_waivers(self):
        content = json.dumps({"schema_version": 1, "waivers": [self.waiver]}).encode()
        with tarfile.open(self.root / "runtime-image-evidence.tar.gz", "w:gz") as archive:
            member = tarfile.TarInfo("runtime-image-evidence/waivers.json")
            member.size = len(content)
            archive.addfile(member, io.BytesIO(content))
        self.runtime["waivers_sha256"] = "sha256:" + hashlib.sha256(content).hexdigest()
        self.runtime["entries"] = [{"reference": self.waiver["reference"], "applied_waivers": [validator.runtime_policy.digest_waiver(self.waiver, self.waiver["ids"][0])]}]

    def validate(self):
        return validator.validate(self.args, self.root, dt.datetime(2026, 10, 7, tzinfo=dt.timezone.utc))

    def test_valid_evidence_requires_three_exact_signatures_and_discloses_deferrals(self):
        result = self.validate()
        self.assertEqual(self.verify.call_count, 3)
        identities = [call.args[2] for call in self.verify.call_args_list]
        self.assertEqual(identities[:2], ["https://github.com/owner/repo/.github/workflows/release.yaml@refs/tags/v1.2.0"] * 2)
        self.assertEqual(identities[2], self.args.rc_identity)
        self.assertEqual(set(result["external_certifications"].values()), {"deferred"})

    def test_recovery_rechecks_expiry_at_publication(self):
        self.validate()
        for day in (6, 7):
            with self.subTest(day=day), self.assertRaisesRegex(ValueError, "expired waiver"):
                validator.validate(self.args, self.root, dt.datetime(2026, 11, day, tzinfo=dt.timezone.utc))

    def test_retained_waivers_must_match_signed_hash(self):
        self.waiver["expires_at"] = "2027-01-01T00:00:00Z"
        self.retain_waivers()  # Keep the original signed runtime report on disk.
        with self.assertRaisesRegex(ValueError, "do not bind"):
            self.validate()

    def test_wrong_release_and_image_scopes_are_rejected(self):
        self.waiver["release_version"] = "v1.2.1"
        self.retain_waivers()
        self.write("runtime-image-evidence", self.runtime)
        with self.assertRaisesRegex(ValueError, "release version"):
            self.validate()
        self.waiver["release_version"] = "v1.2.0"
        self.retain_waivers()
        self.runtime["entries"][0]["reference"] = "example.com/other@sha256:" + "b" * 64
        self.write("runtime-image-evidence", self.runtime)
        with self.assertRaisesRegex(ValueError, "wrong-image"):
            self.validate()

    def test_other_tags_and_untrusted_producers_cannot_use_exception(self):
        for field, value in (("tag", "v1.2.1"), ("rc_identity", "https://github.com/owner/repo/.github/workflows/other.yaml@refs/heads/main")):
            args = copy.copy(self.args)
            setattr(args, field, value)
            with self.subTest(field=field), self.assertRaises(ValueError):
                validator.validate(args, self.root)

    def test_wrong_release_run_commit_manifest_or_failed_restore_is_rejected(self):
        for field, value in (("source_run_id", "999"), ("producer_run_id", "999"), ("source_commit", "b" * 40), ("release_manifest_sha256", "sha256:" + "b" * 64), ("previous_version", "v1.0.0"), ("backup_restore", "failed"), ("clean_restore", "failed"), ("decrypt_proof", "failed"), ("result", "failed")):
            rc = dict(self.rc, **{field: value})
            self.write("rc-rehearsal-evidence", rc)
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.validate()

    def test_runtime_report_must_pass_and_bind_same_manifest(self):
        for field, value in (("result", "failed"), ("vulnerability_qualification", "failed"), ("license_qualification", "passed"), ("release_version", "v1.1.0"), ("release_manifest_sha256", "sha256:" + "b" * 64)):
            self.write("runtime-image-evidence", dict(self.runtime, **{field: value}))
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.validate()

    def test_recovered_metadata_requires_extra_proof_and_keeps_rc_identity(self):
        self.args.repository = validator.metadata.REPOSITORY
        self.args.source_commit = validator.metadata.RECOVERY_COMMIT
        self.args.source_run_id = validator.metadata.RECOVERY_SOURCE_RUN
        self.args.rc_identity = validator.metadata.RECOVERY_IDENTITY
        self.write("release-manifest", {"release": {"version": self.args.tag, "source_commit": self.args.source_commit}})
        digest = validator.approval.sha256(self.root / "release-manifest.json")
        self.runtime["release_manifest_sha256"] = digest
        self.write("runtime-image-evidence", self.runtime)
        self.rc.update(source_commit=self.args.source_commit, source_run_id=self.args.source_run_id, release_manifest_sha256=digest)
        self.write("rc-rehearsal-evidence", self.rc)
        chart = self.root / "astronomer-1.2.0.tgz"
        chart.write_bytes(b"chart")
        self.write("release-recovery", dict(schema_version=1, tag=self.args.tag, source_commit=self.args.source_commit,
                   source_run_id=self.args.source_run_id, producer_run_id="456", producer_commit="b" * 40,
                   files={name: validator.metadata.sha(self.root / name) for name in validator.metadata.FILES}))
        (self.root / "release-recovery.sigstore.json").write_text("bundle")
        with patch.object(validator.metadata, "verify_signature") as proof_verify:
            result = self.validate()
            proof_verify.assert_called_once()
            self.assertEqual(proof_verify.call_args.args[2], validator.metadata.RECOVERY_IDENTITY)
            self.assertEqual([call.args[2] for call in self.verify.call_args_list], [validator.metadata.RECOVERY_IDENTITY] * 3)
            self.assertEqual(result["release_recovery_sha256"], validator.approval.sha256(self.root / "release-recovery.json"))
            proof_verify.side_effect = subprocess.CalledProcessError(1, "cosign")
            with self.assertRaises(subprocess.CalledProcessError):
                self.validate()

    def test_invalid_signature_aborts_publication(self):
        self.verify.side_effect = subprocess.CalledProcessError(1, "cosign")
        with self.assertRaises(subprocess.CalledProcessError):
            self.validate()


if __name__ == "__main__":
    unittest.main()
