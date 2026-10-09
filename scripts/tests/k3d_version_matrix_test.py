"""Actual wrapper fixtures ensure no arbitrary existing management plane is contacted."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).parents[2]


class OwnedMatrixTests(unittest.TestCase):
    def invoke(self, args, extra=None):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "scripts").mkdir()
            shutil.copy(ROOT / "scripts/k3d-version-matrix-test.sh", root / "scripts/runner.sh")
            runner = root / "scripts/test-live-browser.sh"
            marker = root / "calls"
            runner.write_text("#!/bin/bash\nprintf '%s\\n' \"$KUBERNETES_QUALIFICATION_MINOR\" >> \"$MATRIX_CALLS\"\n")
            runner.chmod(0o700)
            env = os.environ.copy()
            for key in ("ASTRO_URL", "ASTRO_PASSWORD", "ASTRO_EMAIL", "AGENT_IMAGE", "K3S_IMAGE"):
                env.pop(key, None)
            env.update(MATRIX_CALLS=str(marker), LIVE_BROWSER_ARTIFACT_DIR=str(root / "artifacts"))
            env.update(extra or {})
            result = subprocess.run(["bash", str(root / "scripts/runner.sh"), *args], env=env, capture_output=True, text=True, timeout=10)
            calls = marker.read_text().splitlines() if marker.exists() else []
            return result, calls

    def test_declared_minors_create_separate_owned_stacks(self):
        result, calls = self.invoke(["1.36", "1.37"])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, ["1.36", "1.37"])

    def test_historical_candidate_commands_fail_truthfully(self):
        for version in ("v1.33.5-k3s1", "v1.34.1-k3s1", "v1.35.0-k3s1", "1.38"):
            result, calls = self.invoke([version])
            self.assertEqual(result.returncode, 2)
            self.assertEqual(calls, [])

    def test_external_credentials_and_images_never_reach_runner(self):
        for key in ("ASTRO_URL", "ASTRO_PASSWORD", "ASTRO_EMAIL", "AGENT_IMAGE", "K3S_IMAGE"):
            result, calls = self.invoke(["1.36"], {key: "fixture-value"})
            self.assertEqual(result.returncode, 2)
            self.assertEqual(calls, [])

    def test_duplicate_lanes_fail_before_runner(self):
        result, calls = self.invoke(["1.37", "1.37"])
        self.assertEqual(result.returncode, 2)
        self.assertEqual(calls, [])

    def test_validation_is_side_effect_free(self):
        result, calls = self.invoke(["--validate-only"])
        self.assertEqual(result.returncode, 0)
        self.assertEqual(calls, [])


if __name__ == "__main__":
    unittest.main()
