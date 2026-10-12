#!/usr/bin/env python3
import copy
import datetime as dt
import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("waivers", ROOT / "scripts/prepare-upstream-binary-waivers.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class UpstreamBinaryWaiversTest(unittest.TestCase):
    def setUp(self):
        self.policy = json.loads(module.POLICY.read_text())
        self.digest = self.policy["binary_sha256"]["linux/amd64"]
        self.now = dt.date(2026, 10, 11)

    def check(self, **kwargs):
        return module.validated_ignores(kwargs.get("policy", self.policy), kwargs.get("release", "v2.0.1"),
                                        kwargs.get("digest", self.digest), kwargs.get("now", self.now))

    def test_exact_official_bytes_and_path_only(self):
        result = self.check()
        self.assertEqual({v["id"] for v in result["vulnerabilities"]}, set(self.policy["ids"]))
        self.assertTrue(all(v["paths"] == ["usr/local/bin/kubectl"] for v in result["vulnerabilities"]))
        authority = json.loads((ROOT / "deploy/toolchain/versions.json").read_text())
        kubectl = next(c for c in authority["components"] if c["id"] == "kubectl-shell")
        self.assertEqual(self.policy["binary_sha256"], {a["platform"]: a["digest"] for a in kubectl["executableArtifacts"]})
        self.assertEqual(self.policy["version"], "v" + kubectl["selected"])

    def test_rebuilt_or_replaced_binary_is_rejected(self):
        with self.assertRaises(ValueError):
            self.check(digest="sha256:" + "0" * 64)

    def test_future_releases_are_not_waived(self):
        with self.assertRaises(ValueError):
            self.check(release="v2.0.2")

    def test_expiry_is_enforced(self):
        with self.assertRaises(ValueError):
            self.check(now=dt.date(2026, 11, 6))

    def test_broader_path_is_rejected(self):
        policy = copy.deepcopy(self.policy)
        policy["path"] = "*"
        with self.assertRaises(ValueError):
            self.check(policy=policy)


if __name__ == "__main__":
    unittest.main()
