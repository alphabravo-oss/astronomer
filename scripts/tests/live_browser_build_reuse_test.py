#!/usr/bin/env python3
"""Local build reuse must reject drift before creating a test stack."""
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("reuse", Path(__file__).parents[1] / "live-browser-build-reuse.py")
reuse = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(reuse)


class BuildReuseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.scope = Path(self.temp.name)
        self.root = self.scope / "source"
        self.root.mkdir()
        self.git("init", "-q")
        self.git("config", "user.email", "fixture@example.invalid")
        self.git("config", "user.name", "Fixture")
        self.put(".gitignore", "frontend/dist/\n")
        self.put("go.mod", "module fixture\n")
        self.put("frontend/package-lock.json", "{}\n")
        self.put("frontend/tests/e2e-live/refresh.live.spec.ts", "old fixture\n")
        self.git("add", ".")
        self.git("commit", "-qm", "application")
        self.base = self.git("rev-parse", "HEAD")
        self.identity = {"schema_version": 2, "source_file_count": 4, "source_tree_sha256": "a" * 64}
        self.run = self.scope / "run"
        self.run.mkdir()
        (self.run / "matrix-receipt.json").write_text(json.dumps({"sourceCommit": self.base, "sourceStable": True, "sourceBefore": self.identity, "sourceAfter": self.identity}))
        for name in reuse.BINARIES:
            path = self.run / "browser/bin" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(name.encode())
            path.chmod(0o755)
        self.put("frontend/dist/index.html", "qualified frontend")
        self.reference = self.scope / "reference.json"
        self.addCleanup(patch.stopall)
        patch.object(reuse, "source_identity", return_value=self.identity).start()
        patch.object(reuse, "bound_binary", return_value="b" * 64).start()
        reuse.capture(self.root, self.run, self.reference, self.scope)
        self.document = json.loads(self.reference.read_text())

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, text=True, stderr=subprocess.DEVNULL).strip()

    def put(self, path, value):
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(value)

    def commit(self, path, value):
        self.put(path, value)
        self.git("add", path)
        self.git("commit", "-qm", "candidate")

    def check(self, document=None):
        return reuse.check(document or self.document, self.root, self.scope)

    def test_fixture_delta_keeps_application_and_fixture_identities_separate(self):
        self.commit("frontend/tests/e2e-live/refresh.live.spec.ts", "commit readiness\n")
        evidence = self.check()
        self.assertEqual(evidence["applicationCommit"], self.base)
        self.assertNotEqual(evidence["fixtureCommit"], self.base)
        self.assertEqual(evidence["fixtureOnlyPaths"], ["frontend/tests/e2e-live/refresh.live.spec.ts"])

    def test_application_or_lock_or_config_changes_are_rejected(self):
        for path in ("go.mod", "frontend/package-lock.json", "frontend/vite.config.ts", "internal/handler/auth.go"):
            with self.subTest(path=path):
                self.git("reset", "--hard", self.base)
                self.commit(path, "changed\n")
                with self.assertRaisesRegex(ValueError, "application source"):
                    self.check()

    def test_dirty_checkout_is_rejected(self):
        self.put("go.mod", "uncommitted\n")
        with self.assertRaisesRegex(ValueError, "clean frozen"):
            self.check()

    def test_hidden_worktree_byte_drift_is_rejected(self):
        self.git("update-index", "--assume-unchanged", "go.mod")
        self.put("go.mod", "hidden drift\n")
        with self.assertRaisesRegex(ValueError, "actual application source"):
            self.check()

    def test_unknown_schema_is_rejected(self):
        document = copy.deepcopy(self.document)
        document["schemaVersion"] = "future"
        with self.assertRaisesRegex(ValueError, "schema"):
            self.check(document)

    def test_extra_field_is_rejected(self):
        document = copy.deepcopy(self.document)
        document["override"] = True
        with self.assertRaisesRegex(ValueError, "schema"):
            self.check(document)

    def test_binary_tamper_is_rejected(self):
        Path(self.document["binaries"]["server"]["path"]).write_bytes(b"tampered")
        with self.assertRaisesRegex(ValueError, "binary changed"):
            self.check()

    def test_missing_binary_inventory_is_rejected(self):
        del self.document["binaries"]["worker"]
        with self.assertRaisesRegex(ValueError, "inventory"):
            self.check()

    def test_build_revision_mismatch_is_rejected(self):
        with patch.object(reuse, "bound_binary", side_effect=ValueError("revision mismatch")):
            with self.assertRaisesRegex(ValueError, "revision mismatch"):
                self.check()

    def test_frontend_tamper_is_rejected(self):
        self.put("frontend/dist/index.html", "changed frontend")
        with self.assertRaisesRegex(ValueError, "frontend build changed"):
            self.check()

    def test_frontend_symlink_is_rejected(self):
        (self.root / "frontend/dist/link").symlink_to("index.html")
        with self.assertRaisesRegex(ValueError, "symlinks"):
            self.check()

    def test_receipt_tamper_is_rejected(self):
        (self.run / "matrix-receipt.json").write_text("{}")
        with self.assertRaisesRegex(ValueError, "receipt changed"):
            self.check()

    def test_outside_scope_or_symlink_artifact_is_rejected(self):
        for path in (Path("/outside/fixture"), self.scope / "binary-link"):
            with self.subTest(path=path):
                if path.parent == self.scope:
                    path.symlink_to(self.run / "browser/bin/server")
                with self.assertRaisesRegex(ValueError, "owned qualification"):
                    reuse.protected(path, self.scope)

    def test_materialize_verifies_and_refuses_existing_outputs(self):
        dist = self.root / "frontend/dist"
        old_dist = self.scope / "original-dist"
        dist.rename(old_dist)
        self.document["frontend"]["path"] = str(old_dist)
        artifact = self.scope / "new-run"
        artifact.mkdir()
        reuse.materialize(self.document, self.root, artifact, self.scope)
        evidence = json.loads((artifact / "build-reuse.json").read_text())
        self.assertEqual(evidence["applicationCommit"], self.base)
        self.assertEqual((artifact / "bin/server").read_bytes(), b"server")
        with self.assertRaisesRegex(ValueError, "existing outputs"):
            reuse.materialize(self.document, self.root, artifact, self.scope)


if __name__ == "__main__":
    unittest.main()
