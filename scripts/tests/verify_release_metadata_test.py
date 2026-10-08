import copy
import importlib.util
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("metadata", Path(__file__).resolve().parents[1] / "verify-release-metadata.py")
metadata = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(metadata)


class RecoveryMetadataTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in metadata.FILES:
            (self.root / name).write_text(name)
        self.manifest = {"release": {"version": metadata.RECOVERY_TAG, "source_commit": metadata.RECOVERY_COMMIT}}
        (self.root / "release-manifest.json").write_text(json.dumps(self.manifest))
        self.record = dict(schema_version=1, tag=metadata.RECOVERY_TAG, source_commit=metadata.RECOVERY_COMMIT,
                           source_run_id=metadata.RECOVERY_SOURCE_RUN, producer_run_id="999", producer_commit="b" * 40,
                           files={name: metadata.sha(self.root / name) for name in metadata.FILES})
        (self.root / "release-recovery.sigstore.json").write_text("bundle")
        self.write(self.record)
        mock = patch.object(metadata, "verify_signature")
        self.verify = mock.start()
        self.addCleanup(mock.stop)

    def write(self, record):
        (self.root / "release-recovery.json").write_text(json.dumps(record))

    def validate(self, **kwargs):
        return metadata.metadata_identity(self.root, metadata.RECOVERY_TAG, **kwargs)

    def test_exact_recovery_uses_constant_trusted_identity(self):
        self.assertEqual(self.validate(source_commit=metadata.RECOVERY_COMMIT, source_run_id=metadata.RECOVERY_SOURCE_RUN), metadata.RECOVERY_IDENTITY)
        self.verify.assert_called_once_with(self.root / "release-recovery.json", self.root / "release-recovery.sigstore.json", metadata.RECOVERY_IDENTITY)

    def test_original_release_retains_original_identity(self):
        (self.root / "release-recovery.json").unlink()
        (self.root / "release-recovery.sigstore.json").unlink()
        self.assertEqual(self.validate(), "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/v1.2.0")
        self.verify.assert_not_called()

    def test_incomplete_proof_and_other_tag_fail(self):
        with self.assertRaises(ValueError):
            metadata.metadata_identity(self.root, "v1.2.1")
        (self.root / "release-recovery.sigstore.json").unlink()
        with self.assertRaises(ValueError):
            self.validate()

    def test_invalid_signature_never_selects_recovery_identity(self):
        self.verify.side_effect = subprocess.CalledProcessError(1, "cosign")
        with self.assertRaises(subprocess.CalledProcessError):
            self.validate()

    def test_record_cannot_change_release_or_add_signer_override(self):
        for field, value in (("schema_version", 2), ("tag", "v1.2.1"), ("source_commit", "c" * 40),
                             ("source_run_id", "888"), ("producer_run_id", "bad"), ("producer_commit", "bad"),
                             ("certificate_identity", "attacker")):
            with self.subTest(field=field):
                self.write(dict(self.record, **{field: value}))
                with self.assertRaises(ValueError):
                    self.validate()

    def test_callers_expected_source_is_checked(self):
        for kwargs in ({"source_commit": "d" * 40}, {"source_run_id": "888"}):
            with self.subTest(kwargs=kwargs), self.assertRaises(ValueError):
                self.validate(**kwargs)

    def test_every_present_metadata_file_is_hash_bound(self):
        for name in metadata.FILES:
            path = self.root / name
            original = path.read_bytes()
            path.write_bytes(original + b"altered")
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, "content mismatch"):
                self.validate()
            path.write_bytes(original)

    def test_file_set_is_closed_and_digests_are_valid(self):
        for change in ("missing", "extra", "bad_digest"):
            record = copy.deepcopy(self.record)
            if change == "missing":
                record["files"].pop("runtime-image-evidence.tar.gz")
            elif change == "extra":
                record["files"]["other"] = "sha256:" + "a" * 64
            else:
                record["files"]["release-manifest.json"] = "invalid"
            self.write(record)
            with self.subTest(change=change), self.assertRaises(ValueError):
                self.validate()

    def test_renamed_manifest_must_match_proof_even_with_valid_canonical_file(self):
        alias = self.root / "operator-manifest.json"
        alias.write_bytes((self.root / "release-manifest.json").read_bytes())
        self.assertEqual(self.validate(manifest_path=alias), metadata.RECOVERY_IDENTITY)
        alias.write_text(json.dumps(dict(self.manifest, unexpected="modified payload")))
        with self.assertRaisesRegex(ValueError, "content mismatch"):
            self.validate(manifest_path=alias)

    def test_airgap_checks_supplied_manifest_instead_of_neighboring_file(self):
        spec = importlib.util.spec_from_file_location("kit", Path(__file__).resolve().parents[1] / "airgap-kit.py")
        kit = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(kit)
        alias = self.root / "operator-manifest.json"
        alias.write_bytes((self.root / "release-manifest.json").read_bytes())
        signature = self.root / "operator.sigstore.json"
        signature.write_text("bundle")
        with patch.object(kit.subprocess, "run"):
            kit.verify_release_manifest(alias, signature, self.manifest)
            alias.write_text(json.dumps(dict(self.manifest, unexpected="modified payload")))
            with self.assertRaisesRegex(kit.KitError, "supplied manifest"):
                kit.verify_release_manifest(alias, signature, self.manifest)

    def test_manifest_only_offline_handoff_requires_exact_source(self):
        for name in metadata.FILES - {"release-manifest.json"}:
            (self.root / name).unlink()
        self.assertEqual(self.validate(), metadata.RECOVERY_IDENTITY)
        self.manifest["release"]["source_commit"] = "e" * 40
        path = self.root / "release-manifest.json"
        path.write_text(json.dumps(self.manifest))
        self.record["files"][path.name] = metadata.sha(path)
        self.write(self.record)
        with self.assertRaisesRegex(ValueError, "manifest source mismatch"):
            self.validate()


if __name__ == "__main__":
    unittest.main()
