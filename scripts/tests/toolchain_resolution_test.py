import copy
import datetime as dt
import hashlib
import importlib.util
import json
import pathlib
import re
import sys
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
SPEC = importlib.util.spec_from_file_location("resolution", ROOT / "scripts/toolchain-resolution.py")
resolution = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(resolution)


class ResolutionTests(unittest.TestCase):
    def setUp(self):
        self.authority = resolution.toolchain.AUTHORITY.read_bytes()
        self.document = json.loads(self.authority)
        self.now = dt.datetime(2026, 10, 9, 23, 59, tzinfo=dt.timezone.utc)
        self.receipt = {"schemaVersion": 1, "authorityDigest": "sha256:" + hashlib.sha256(self.authority).hexdigest(), "generatedAt": "2026-10-09T23:00:00Z", "expiresAt": "2026-10-10T00:00:00Z", "sourceCommit": "a" * 40, "components": resolution.bindings(self.document)}

    def test_exact_authority_binding(self):
        resolution.verify_binding(self.receipt, self.document, self.authority, self.now)
        with self.assertRaisesRegex(ValueError, "authority digest"):
            resolution.verify_binding(self.receipt, self.document, self.authority + b"\n", self.now)

    def test_changed_artifact_and_removed_component(self):
        c = next(c for c in self.receipt["components"] if c["artifactDigests"])
        c["artifactDigests"][0] = "sha256:" + "0" * 64
        with self.assertRaisesRegex(ValueError, "bindings"):
            resolution.verify_binding(self.receipt, self.document, self.authority, self.now)
        self.setUp()
        self.receipt["components"].pop()
        with self.assertRaisesRegex(ValueError, "bindings"):
            resolution.verify_binding(self.receipt, self.document, self.authority, self.now)

    def test_expired_and_overlong_resolution(self):
        self.receipt["expiresAt"] = "2026-10-09T23:01:00Z"
        with self.assertRaisesRegex(ValueError, "expired"):
            resolution.verify_binding(self.receipt, self.document, self.authority, self.now)
        self.receipt["expiresAt"] = "2027-01-01T00:00:00Z"
        with self.assertRaisesRegex(ValueError, "lifetime"):
            resolution.verify_binding(self.receipt, self.document, self.authority, self.now)

    def test_signer_identity_negative_fixtures(self):
        policy = re.compile(resolution.IDENTITY)
        prefix = "https://github.com/alphabravo-oss/astronomer/.github/workflows/"
        self.assertIsNotNone(policy.fullmatch(prefix + "toolchain-resolution.yaml@refs/heads/main"))
        self.assertIsNotNone(policy.fullmatch(prefix + "release.yaml@refs/tags/v1.2.2"))
        for identity in [prefix + "pr-validation.yaml@refs/heads/main", prefix + "toolchain-resolution.yaml@refs/heads/feature", prefix + "release.yaml@refs/tags/v1.2.2-rc.1", prefix.replace("alphabravo-oss", "attacker") + "toolchain-resolution.yaml@refs/heads/main", prefix + "release.yaml@refs/tags/v1.2.2/extra"]:
            self.assertIsNone(policy.fullmatch(identity), identity)


if __name__ == "__main__":
    unittest.main()
