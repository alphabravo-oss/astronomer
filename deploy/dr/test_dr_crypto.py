from __future__ import annotations

import importlib.util
import io
import json
import datetime as dt
from pathlib import Path
import tarfile
import tempfile
import unittest

from cryptography.hazmat.primitives import hashes, padding
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
from cryptography.hazmat.primitives.kdf.pbkdf2 import PBKDF2HMAC


MODULE_PATH = Path(__file__).with_name("dr_crypto.py")
SPEC = importlib.util.spec_from_file_location("dr_crypto", MODULE_PATH)
assert SPEC and SPEC.loader
DR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DR)


class DRCryptoTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.passphrase = self.root / "passphrase"
        self.passphrase.write_bytes(b"correct horse battery staple, but generated")
        self.context = {"release": "astronomer", "source": "prod-east"}

    def tearDown(self) -> None:
        self.temp.cleanup()

    def test_envelope_round_trip_and_tamper_fail_before_output(self) -> None:
        plaintext = self.root / "dump.pgcustom"
        plaintext.write_bytes((b"postgres-custom-dump\0" * 100_000) + b"end")
        encrypted = self.root / "dump.aead"
        restored = self.root / "restored.pgcustom"
        DR.encrypt_file(plaintext, encrypted, self.passphrase, "database-dump", self.context)
        DR.decrypt_file(encrypted, restored, self.passphrase, "database-dump", self.context)
        self.assertEqual(restored.read_bytes(), plaintext.read_bytes())

        tampered = self.root / "tampered.aead"
        content = bytearray(encrypted.read_bytes())
        content[-20] ^= 1
        tampered.write_bytes(content)
        rejected = self.root / "rejected.pgcustom"
        with self.assertRaisesRegex(DR.DRError, "authentication failed"):
            DR.decrypt_file(tampered, rejected, self.passphrase, "database-dump", self.context)
        self.assertFalse(rejected.exists())

    def test_envelope_binds_purpose_and_context(self) -> None:
        source = self.root / "keys.tar"
        source.write_bytes(b"bundle")
        encrypted = self.root / "keys.aead"
        DR.encrypt_file(source, encrypted, self.passphrase, "key-bundle", self.context)
        with self.assertRaisesRegex(DR.DRError, "purpose"):
            DR.decrypt_file(encrypted, self.root / "wrong-purpose", self.passphrase, "database-dump", self.context)
        with self.assertRaisesRegex(DR.DRError, "context"):
            DR.decrypt_file(
                encrypted,
                self.root / "wrong-context",
                self.passphrase,
                "key-bundle",
                {"release": "other", "source": "prod-east"},
            )

    def manifest_args(self, archive: Path, key_bundle: Path, output: Path):
        class Args:
            pass

        args = Args()
        args.archive = archive
        args.archive_key = "astronomer-pg/astronomer/daily/2026.pgcustom.aead"
        args.key_bundle = key_bundle
        args.key_bundle_key = "astronomer-pg/astronomer/keys/2026.keys.tar.aead"
        args.passphrase_file = self.passphrase
        args.release_name = "astronomer"
        args.release_namespace = "astronomer-system"
        args.chart_version = "1.1.0"
        args.source_identity = "prod-east"
        args.database_schema_version = 47
        args.created_at = "2026-09-17T03:00:00Z"
        args.output = output
        return args

    def verify_args(self, manifest: Path, archive: Path | None = None, key_bundle: Path | None = None):
        class Args:
            pass

        args = Args()
        args.input = manifest
        args.passphrase_file = self.passphrase
        args.archive_file = archive
        args.key_bundle_file = key_bundle
        args.expect_release_name = "astronomer"
        args.expect_release_namespace = "astronomer-system"
        args.expect_source_identity = "prod-east"
        args.expect_prefix = "astronomer-pg/astronomer"
        args.print_field = None
        return args

    def test_manifest_authenticates_identity_object_names_and_payloads(self) -> None:
        archive = self.root / "dump.aead"
        key_bundle = self.root / "keys.aead"
        archive.write_bytes(b"encrypted dump")
        key_bundle.write_bytes(b"encrypted keys")
        manifest = self.root / "manifest.json"
        DR.create_manifest(self.manifest_args(archive, key_bundle, manifest))
        DR.verify_manifest(self.verify_args(manifest, archive, key_bundle))

        document = json.loads(manifest.read_text())
        document["archive"]["key"] = "astronomer-pg/other/daily/stolen.aead"
        manifest.write_text(json.dumps(document))
        with self.assertRaisesRegex(DR.DRError, "authentication failed"):
            DR.verify_manifest(self.verify_args(manifest, archive, key_bundle))

    def test_manifest_rejects_payload_substitution_and_wrong_source(self) -> None:
        archive = self.root / "dump.aead"
        key_bundle = self.root / "keys.aead"
        archive.write_bytes(b"encrypted dump")
        key_bundle.write_bytes(b"encrypted keys")
        manifest = self.root / "manifest.json"
        DR.create_manifest(self.manifest_args(archive, key_bundle, manifest))
        archive.write_bytes(b"replacement dump")
        with self.assertRaisesRegex(DR.DRError, "digest/size mismatch"):
            DR.verify_manifest(self.verify_args(manifest, archive, key_bundle))
        args = self.verify_args(manifest)
        args.expect_source_identity = "prod-west"
        with self.assertRaisesRegex(DR.DRError, "identity"):
            DR.verify_manifest(args)

    def key_source(self) -> Path:
        source = self.root / "key-source"
        source.mkdir()
        (source / "SECRET_KEY").write_text("jwt key")
        (source / "ASTRONOMER_ENCRYPTION_KEY").write_text("fernet key")
        (source / "OIDC_SECRET").write_text("oauth key")
        return source

    def test_key_bundle_round_trip_is_closed_and_deterministic(self) -> None:
        source = self.key_source()
        first = self.root / "first.tar"
        second = self.root / "second.tar"
        DR.create_key_bundle(source, first)
        DR.create_key_bundle(source, second)
        self.assertEqual(first.read_bytes(), second.read_bytes())
        extracted = self.root / "extracted"
        DR.extract_key_bundle(first, extracted)
        self.assertEqual((extracted / "ASTRONOMER_ENCRYPTION_KEY").read_text(), "fernet key")
        self.assertEqual((extracted / "OIDC_SECRET").read_text(), "oauth key")

    def test_key_bundle_rejects_links_traversal_and_duplicate_members(self) -> None:
        malicious = self.root / "malicious.tar"
        with tarfile.open(malicious, "w") as archive:
            for name in ("SECRET_KEY", "ASTRONOMER_ENCRYPTION_KEY", "../escape"):
                data = b"secret"
                info = tarfile.TarInfo(name)
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
        with self.assertRaisesRegex(DR.DRError, "unsafe member path"):
            DR.extract_key_bundle(malicious, self.root / "bad-output")
        self.assertFalse((self.root / "escape").exists())

        source = self.key_source()
        (source / "link").symlink_to(source / "SECRET_KEY")
        with self.assertRaisesRegex(DR.DRError, "regular files"):
            DR.create_key_bundle(source, self.root / "linked.tar")

    def test_legacy_cbc_is_read_only_and_sunset_bounded(self) -> None:
        plaintext = b"legacy tar payload"
        salt = b"12345678"
        material = PBKDF2HMAC(
            algorithm=hashes.SHA256(), length=48, salt=salt, iterations=10_000
        ).derive(self.passphrase.read_bytes())
        padder = padding.PKCS7(128).padder()
        padded = padder.update(plaintext) + padder.finalize()
        encryptor = Cipher(algorithms.AES(material[:32]), modes.CBC(material[32:])).encryptor()
        legacy = self.root / "legacy.keys.tar.enc"
        legacy.write_bytes(b"Salted__" + salt + encryptor.update(padded) + encryptor.finalize())

        restored = self.root / "legacy.keys.tar"
        DR.legacy_decrypt_key_bundle(
            legacy, restored, self.passphrase, dt.date.today() + dt.timedelta(days=1)
        )
        self.assertEqual(restored.read_bytes(), plaintext)
        with self.assertRaisesRegex(DR.DRError, "expired"):
            DR.legacy_decrypt_key_bundle(
                legacy, self.root / "expired", self.passphrase, dt.date.today()
            )


if __name__ == "__main__":
    unittest.main()
