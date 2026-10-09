import copy
import datetime as dt
import hashlib
import importlib.util
import json
import pathlib
import re
import sys
import unittest
import tempfile
import subprocess
import os

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

    def test_nonpositive_lifetime_and_schema(self):
        self.receipt["expiresAt"] = self.receipt["generatedAt"]
        with self.assertRaisesRegex(ValueError, "precedes generation"):
            resolution.verify_binding(self.receipt, self.document, self.authority, self.now)
        self.setUp()
        self.receipt["schemaVersion"] = 2
        with self.assertRaisesRegex(ValueError, "unsupported resolution schema"):
            resolution.verify_binding(self.receipt, self.document, self.authority, self.now)

    def test_actual_verifier_cli_rejects_failure_missing_bundle_schema_and_source(self):
        with tempfile.TemporaryDirectory() as directory:
            directory = pathlib.Path(directory)
            record = directory / "record.json"
            bundle = directory / "bundle.json"
            bundle.write_text("{}")
            cosign = directory / "cosign"
            cosign.write_text("#!/bin/sh\nexit 42\n")
            cosign.chmod(0o755)
            env = dict(os.environ, PATH=str(directory) + os.pathsep + os.environ["PATH"], GOTOOLCHAIN="go1.27.2", GOMAXPROCS="2", GOFLAGS="-p=2")
            base = [sys.executable, str(ROOT / "scripts/toolchain-resolution.py")]
            generated = subprocess.run(base + ["--generate", "--record", str(record)], capture_output=True, text=True, env=env)
            self.assertEqual(generated.returncode, 0, generated.stderr)
            args = base + ["--verify", "--record", str(record)]
            missing = subprocess.run(args, capture_output=True, text=True, env=env)
            self.assertNotEqual(missing.returncode, 0)
            self.assertIn("signed Sigstore resolution bundle", missing.stderr)
            failed = subprocess.run(args + ["--bundle", str(bundle)], capture_output=True, text=True, env=env)
            self.assertNotEqual(failed.returncode, 0)
            self.assertIn("42", failed.stderr)
            cosign.write_text("#!/bin/sh\nexit 0\n")
            receipt = json.loads(record.read_text())
            receipt["sourceCommit"] = "0" * 40
            record.write_text(json.dumps(receipt))
            wrong_source = subprocess.run(args + ["--bundle", str(bundle)], capture_output=True, text=True, env=env)
            self.assertNotEqual(wrong_source.returncode, 0)
            self.assertIn("different source commit", wrong_source.stderr)
            receipt["schemaVersion"] = 99
            record.write_text(json.dumps(receipt))
            wrong_schema = subprocess.run(args + ["--bundle", str(bundle)], capture_output=True, text=True, env=env)
            self.assertNotEqual(wrong_schema.returncode, 0)
            self.assertIn("schema", wrong_schema.stderr.lower())

    def test_dedicated_signing_workflow_contract(self):
        workflow = ROOT / ".github/workflows/toolchain-resolution.yaml"
        raw = subprocess.check_output(["go", "run", "./deploy/toolchain/check", "--yaml-json"], input=workflow.read_bytes(), cwd=ROOT)
        document = json.loads(raw)
        self.assertEqual(set(document["on"]), {"workflow_dispatch"})
        self.assertEqual(document["permissions"], {"contents": "read"})
        job = document["jobs"]["resolution"]
        self.assertEqual(job["permissions"], {"contents": "read", "id-token": "write"})
        self.assertEqual(job["if"], "github.repository == 'alphabravo-oss/astronomer' && github.ref == 'refs/heads/main'")
        runs = [step.get("run", "") for step in job["steps"]]
        self.assertTrue(any("cosign sign-blob --yes --bundle" in run for run in runs))
        self.assertTrue(any("versions-release-check" in run for run in runs))
        for step in job["steps"]:
            if "uses" in step:
                self.assertRegex(step["uses"], r"@[a-f0-9]{40}$")

    def test_signer_identity_negative_fixtures(self):
        policy = re.compile(resolution.IDENTITY)
        prefix = "https://github.com/alphabravo-oss/astronomer/.github/workflows/"
        self.assertIsNotNone(policy.fullmatch(prefix + "toolchain-resolution.yaml@refs/heads/main"))
        self.assertIsNotNone(policy.fullmatch(prefix + "release.yaml@refs/tags/v1.2.2"))
        for identity in [prefix + "pr-validation.yaml@refs/heads/main", prefix + "toolchain-resolution.yaml@refs/heads/feature", prefix + "release.yaml@refs/tags/v1.2.2-rc.1", prefix.replace("alphabravo-oss", "attacker") + "toolchain-resolution.yaml@refs/heads/main", prefix + "release.yaml@refs/tags/v1.2.2/extra"]:
            self.assertIsNone(policy.fullmatch(identity), identity)


if __name__ == "__main__":
    unittest.main()
