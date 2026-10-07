import copy
import importlib.util
import json
import subprocess
import tempfile
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
        self.rc = dict(schema_version=1, result="passed", target_version=self.args.tag, previous_version="v1.1.0", source_commit=self.args.source_commit, source_run_id="123", producer_run_id="456", release_manifest_sha256=digest, upgrade_evidence_sha256=digest, backup_manifest_sha256=digest, backup_restore="passed", decrypt_proof="passed", clean_restore="passed", destructive_fence="owned_disposable_k3d", started_at="2026-10-07T12:00:00Z", completed_at="2026-10-07T12:10:00Z")
        self.write("runtime-image-evidence", self.runtime)
        self.write("rc-rehearsal-evidence", self.rc)
        mock = patch.object(validator.approval, "verify_signature")
        self.verify = mock.start()
        self.addCleanup(mock.stop)

    def write(self, name, value):
        (self.root / f"{name}.json").write_text(json.dumps(value))

    def test_valid_evidence_requires_three_exact_signatures_and_discloses_deferrals(self):
        result = validator.validate(self.args, self.root)
        self.assertEqual(self.verify.call_count, 3)
        identities = [call.args[2] for call in self.verify.call_args_list]
        self.assertEqual(identities[:2], ["https://github.com/owner/repo/.github/workflows/release.yaml@refs/tags/v1.2.0"] * 2)
        self.assertEqual(identities[2], self.args.rc_identity)
        self.assertEqual(set(result["external_certifications"].values()), {"deferred"})

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
                validator.validate(self.args, self.root)

    def test_runtime_report_must_pass_and_bind_same_manifest(self):
        for field, value in (("result", "failed"), ("vulnerability_qualification", "failed"), ("license_qualification", "passed"), ("release_version", "v1.1.0"), ("release_manifest_sha256", "sha256:" + "b" * 64)):
            self.write("runtime-image-evidence", dict(self.runtime, **{field: value}))
            with self.subTest(field=field), self.assertRaises(ValueError):
                validator.validate(self.args, self.root)

    def test_invalid_signature_aborts_publication(self):
        self.verify.side_effect = subprocess.CalledProcessError(1, "cosign")
        with self.assertRaises(subprocess.CalledProcessError):
            validator.validate(self.args, self.root)


if __name__ == "__main__":
    unittest.main()
