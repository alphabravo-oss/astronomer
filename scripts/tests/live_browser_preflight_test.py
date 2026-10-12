"""Run actual live-runner preflight with tools that refuse all mutations."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).parents[2]


class LivePreflightTests(unittest.TestCase):
    def run_preflight(self, *, image=None, client="v1.34.9", minor="1.36"):
        with tempfile.TemporaryDirectory() as temporary:
            work = Path(temporary)
            marker = work / "forbidden-command"
            for tool in "base64 curl docker git go helm k3d npm openssl setsid sha256sum".split():
                path = work / tool
                path.write_text(f"#!/bin/bash\nprintf '%s' invoked > '{marker}'\nexit 97\n")
                path.chmod(0o700)
            kubectl = work / "kubectl"
            kubectl.write_text("#!/bin/bash\ncat <<'JSON'\n" + json.dumps({"clientVersion": {"gitVersion": client}}) + "\nJSON\n")
            kubectl.chmod(0o700)
            env = os.environ.copy()
            env.pop("K3S_IMAGE", None)
            env.update(PATH=str(work) + ":" + env["PATH"], KUBERNETES_QUALIFICATION_MINOR=minor)
            if image is not None:
                env["K3S_IMAGE"] = image
            result = subprocess.run(["bash", "scripts/test-live-browser.sh"], cwd=ROOT, env=env, capture_output=True, text=True, timeout=10)
            self.assertFalse(marker.exists(), "preflight must reject before any mutation or endpoint access")
            self.assertNotEqual(result.returncode, 0)
            return result.stderr

    def test_arbitrary_image_rejected_before_commands(self):
        self.assertIn("arbitrary Kubernetes image", self.run_preflight(image="rancher/k3s:latest"))

    def test_actual_old_client_rejected_before_cluster_creation(self):
        self.assertIn("within one minor", self.run_preflight())

    def test_undeclared_server_rejected_before_cluster_creation(self):
        self.assertIn("only owned Kubernetes", self.run_preflight(client="v1.37.1", minor="1.35"))


if __name__ == "__main__":
    unittest.main()
