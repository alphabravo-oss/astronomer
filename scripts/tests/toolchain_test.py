import copy
import datetime as dt
import importlib.util
import json
import pathlib
import tempfile
import unittest
import sys
from unittest.mock import patch

ROOT = pathlib.Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
SPEC = importlib.util.spec_from_file_location("toolchain", ROOT / "scripts/toolchain.py")
toolchain = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(toolchain)


class ToolchainTests(unittest.TestCase):
    def setUp(self):
        self.document = json.loads(toolchain.AUTHORITY.read_text())
        self.now = dt.datetime(2026, 10, 9, 23, 59, tzinfo=dt.timezone.utc)

    def test_git_primary_requires_one_dated_final_release(self):
        c = next(c for c in self.document["components"] if c["id"] == "git-flux-fixture")
        markup = b'Latest source release</h4><span class="version">2.56.0</span><a href="/release">Release Notes</a><time>(2026-09-28)</time>'
        with patch.object(toolchain, "fetch_bytes", return_value=(markup, "digest")):
            self.assertEqual(toolchain.resolve(c, self.now)["upstreamStable"], "2.56.0")
        for invalid in (markup + markup, markup.replace(b"2.56.0", b"2.57.0-rc1"), markup.replace(b"2026-09-28", b"2026-10-10")):
            with patch.object(toolchain, "fetch_bytes", return_value=(invalid, "digest")):
                with self.assertRaises(ValueError):
                    toolchain.resolve(c, self.now)

    def test_duplicate_ids(self):
        self.document["components"].append(copy.deepcopy(self.document["components"][0]))
        with self.assertRaisesRegex(ValueError, "duplicate"):
            toolchain.validate(self.document, self.now)

    def test_expired_exception(self):
        component = next(c for c in self.document["components"] if "exception" in c)
        component["exception"]["reviewBy"] = "2026-10-08T00:00:00Z"
        with self.assertRaisesRegex(ValueError, "expired"):
            toolchain.validate(self.document, self.now)

    def test_future_date(self):
        self.document["components"][0]["discovery"]["releasedAt"] = "2027-01-01"
        with self.assertRaisesRegex(ValueError, "future"):
            toolchain.validate(self.document, self.now)

    def test_offset_and_naive_timestamps(self):
        self.assertEqual(toolchain.timestamp("2026-10-09T01:00:00+01:00"), toolchain.timestamp("2026-10-09T00:00:00Z"))
        with self.assertRaisesRegex(ValueError, "explicit UTC offset"):
            toolchain.timestamp("2026-10-09T00:00:00")
        c = self.document["components"][0]
        c["discovery"]["observedAt"] = "2026-10-10T00:30:00-01:00"
        with self.assertRaisesRegex(ValueError, "future"):
            toolchain.validate(self.document, self.now)

    def test_selected_prerelease_and_missing_exception_evidence(self):
        c = self.document["components"][0]
        c["selected"] = "1.0.0-canary.1"
        with self.assertRaisesRegex(ValueError, "non-final selected"):
            toolchain.validate(self.document, self.now)
        self.setUp()
        c = next(c for c in self.document["components"] if "exception" in c)
        c["exception"]["evidence"] = ""
        with self.assertRaisesRegex(ValueError, "incomplete"):
            toolchain.validate(self.document, self.now)

    def test_node_adversarial_ordering_checks_selected_final_platforms(self):
        c = next(c for c in self.document["components"] if c["id"] == "node")
        rows = [{"version": "v27.0.0-rc.1", "date": "2026-10-08", "files": []}, {"version": "v26.11.1", "date": "2026-10-07", "files": ["linux-x64", "linux-arm64"]}, {"version": "v24.0.0", "date": "2026-10-01", "files": []}]
        with patch.object(toolchain, "fetch_json", return_value=(rows, "digest")):
            self.assertEqual(toolchain.resolve(c, self.now)["upstreamStable"], "26.11.1")
        rows[1]["files"] = ["linux-x64"]
        with patch.object(toolchain, "fetch_json", return_value=(rows, "digest")):
            with self.assertRaisesRegex(ValueError, "platforms"):
                toolchain.resolve(c, self.now)

    def test_go_adversarial_ordering_uses_newest_final(self):
        c = next(c for c in self.document["components"] if c["id"] == "go")
        files = [{"os": "linux", "arch": arch, "kind": "archive", "sha256": "a" * 64} for arch in ["amd64", "arm64"]]
        rows = [{"version": "go1.26.6", "stable": True, "files": files}, {"version": "go1.28.0rc1", "stable": False, "files": []}, {"version": "go1.27.2", "stable": True, "files": files}]
        with patch.object(toolchain, "fetch_json", return_value=(rows, "digest")), patch.object(toolchain, "fetch_bytes", return_value=(b"go1.27.2 (released 2026-10-07)", "digest")):
            self.assertEqual(toolchain.resolve(c, self.now)["upstreamStable"], "1.27.2")

    def test_prerelease_discovery(self):
        self.document["components"][0]["discovery"]["version"] = "27.0.0-rc.1"
        with self.assertRaisesRegex(ValueError, "non-final"):
            toolchain.validate(self.document, self.now)

    def test_missing_compatibility_owner(self):
        c = next(c for c in self.document["components"] if c["selected"] != c["discovery"]["version"])
        del c["exception"]
        with self.assertRaisesRegex(ValueError, "without owned"):
            toolchain.validate(self.document, self.now)

    def test_stale_release_discovery(self):
        c = self.document["components"][0]
        c["discovery"]["releasedAt"] = "2026-09-01"
        c["discovery"]["observedAt"] = "2026-09-01T00:00:00Z"
        with self.assertRaisesRegex(ValueError, "stale"):
            toolchain.validate(self.document, self.now, release=True)

    def test_platform_missing(self):
        c = next(c for c in self.document["components"] if c["artifacts"])
        c["artifacts"][0]["platforms"] = ["linux/amd64"]
        with self.assertRaisesRegex(ValueError, "unsupported"):
            toolchain.validate(self.document, self.now)

    def test_digest_contradiction(self):
        c = next(c for c in self.document["components"] if c["artifacts"])
        c["artifacts"][0]["digest"] = "sha256:" + "0" * 64
        with self.assertRaisesRegex(ValueError, "contradiction"):
            toolchain.validate(self.document, self.now)

    def test_artifact_version_is_exact_and_platform_map_matches(self):
        for tag in ['1.27.20-alpine', '1.27.2-malicious', None]:
            self.setUp()
            c = next(c for c in self.document['components'] if c['id'] == 'go')
            a = c['artifacts'][0]
            a['reference'] = 'golang' + (':' + tag if tag else '') + '@' + a['digest']
            with self.assertRaisesRegex(ValueError, 'exactly identify'):
                toolchain.validate(self.document, self.now)
        self.setUp()
        c = next(c for c in self.document['components'] if c['artifacts'])
        c['artifacts'][0]['platformDigests'] = {'linux/amd64': 'sha256:' + 'a' * 64}
        with self.assertRaisesRegex(ValueError, 'platform-map'):
            toolchain.validate(self.document, self.now)

    def test_postgres_support_table_ignores_beta_and_independently_dates_final(self):
        c = next(c for c in self.document['components'] if c['id'] == 'postgres')
        table = b'<tr><td>17</td><td>17.11</td><td>Yes</td></tr><tr><td>19</td><td>19 Beta4</td><td>Yes</td></tr><tr><td>18</td><td>18.6</td><td>Yes</td></tr>'
        dated = b'<strong>Release date:&nbsp;</strong>2026-08-13'
        with patch.object(toolchain, 'fetch_bytes', side_effect=[(table, 'digest'), (dated, 'digest')]):
            self.assertEqual(toolchain.resolve(c, self.now)['upstreamStable'], '18.6')
        with patch.object(toolchain, 'fetch_bytes', side_effect=[(table, 'digest'), (b'<strong>Release date:&nbsp;</strong>2027-01-01', 'digest')]):
            with self.assertRaisesRegex(ValueError, 'future'):
                toolchain.resolve(c, self.now)

    def test_runtime_variants_do_not_accept_prefix_or_arbitrary_suffix(self):
        for name, bad_tag in [("postgres-test-runtime", "18.60-202610050823-standard-trixie"), ("postgres-test-runtime", "18.6-malicious"), ("valkey-test-runtime", "9.1.20-alpine"), ("valkey-test-runtime", "9.1.2-malicious"), ("k3s-supported-test-runtime", "v1.36.50-k3s1"), ("k3s-experimental-test-runtime", "v1.37.1-k3s2")]:
            self.setUp()
            c = next(c for c in self.document["components"] if c["id"] == name)
            artifact = c["artifacts"][0]
            repository = artifact["reference"].split("@",1)[0].rsplit(":",1)[0]
            artifact["reference"] = repository + ":" + bad_tag + "@" + artifact["digest"]
            with self.assertRaisesRegex(ValueError,"exactly identify"):
                toolchain.validate(self.document,self.now)

    def test_source_closure_hash_rejects_changed_tool_graph(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            path = root / 'go.mod'
            path.write_text('qualified')
            import hashlib
            document = {'components': [{'id': 'scanner', 'selected': '2.29.0', 'consumers': [], 'sourceBindings': [{'path': 'go.mod', 'digest': 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()}]}]}
            toolchain.align(document, root)
            path.write_text('different dependency graph')
            with self.assertRaisesRegex(ValueError, 'closure digest'):
                toolchain.align(document, root)

    def test_unsafe_consumer(self):
        c = next(c for c in self.document["components"] if c["consumers"])
        c["consumers"][0]["path"] = "../outside"
        with self.assertRaisesRegex(ValueError, "escapes"):
            toolchain.validate(self.document, self.now)

    def test_consumer_drift_and_deterministic_generation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            path = root / "Dockerfile"
            path.write_text("FROM golang:old\n")
            document = {"components": [{"id": "go", "selected": "1.27.2", "artifacts": [], "consumers": [{"path": "Dockerfile", "kind": "regex", "selector": "^FROM golang:(.*)$", "value": "{version}"}]}]}
            with self.assertRaisesRegex(ValueError, "drift"):
                toolchain.align(document, root)
            toolchain.align(document, root, write=True)
            first = path.read_bytes()
            toolchain.align(document, root, write=True)
            self.assertEqual(first, path.read_bytes())
            toolchain.align(document, root)

    def test_discrimination(self):
        self.assertEqual(toolchain.final_candidates([("1.0.0", "2026-01-01", True), ("2.0.0-rc.1", "2026-01-02", True), ("3.0.0", "2026-01-02", False)], self.now)[0], "1.0.0")

    def test_unavailable_and_rate_limited(self):
        with patch.object(toolchain, "fetch_json", side_effect=toolchain.urllib.error.HTTPError("https://api.github.com", 429, "rate limited", {}, None)):
            report = toolchain.discover({"components": [next(c for c in self.document["components"] if c["upstream"]["kind"] == "npm")]})
        self.assertEqual(report[0]["status"], "unavailable")

    def test_incomplete_and_malicious_metadata(self):
        c = next(c for c in self.document["components"] if c["id"] == "npm")
        with patch.object(toolchain, "fetch_json", return_value=({"dist-tags": {"latest": "12.2.0"}, "versions": {}}, "sha256:" + "0" * 64)):
            with self.assertRaisesRegex(ValueError, "missing"):
                toolchain.resolve(c, self.now)
        with patch.object(toolchain, "fetch_json", return_value=({"dist-tags": {"latest": "12.2.0-rc.1"}, "versions": {"12.2.0-rc.1": {}}, "time": {"12.2.0-rc.1": "2026-10-01"}}, "digest")):
            with self.assertRaisesRegex(ValueError, "final"):
                toolchain.resolve(c, self.now)

    def test_discovery_does_not_mutate_authority(self):
        before = toolchain.AUTHORITY.read_bytes()
        with patch.object(toolchain, "resolve", return_value={"id": "fixture", "status": "current"}):
            toolchain.discover(self.document)
        self.assertEqual(before, toolchain.AUTHORITY.read_bytes())


if __name__ == "__main__":
    unittest.main()
