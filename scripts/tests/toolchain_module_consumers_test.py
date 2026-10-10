import copy
import importlib.util
import json
import pathlib
import re
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
SPEC = importlib.util.spec_from_file_location("toolchain_module_consumers", ROOT / "scripts/toolchain.py")
toolchain = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(toolchain)


class ModuleConsumerTests(unittest.TestCase):
    def setUp(self):
        self.document = json.loads((ROOT / "deploy/toolchain/versions.json").read_text())
        self.components = [c for c in self.document["components"] if c["ecosystem"] == "gomod" and c["id"].startswith("gomod:")]
        self.helm = next(c for c in self.components if c["id"] == "gomod:helm.sh/helm/v4")

    def align_helm(self, body):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "go.mod").write_text(body)
            component = copy.deepcopy(self.helm)
            component.pop("sourceBindings")
            toolchain.align({"components": [component]}, root)

    def test_direct_and_indirect_requirements_match_exact_version(self):
        for suffix in ("", " // indirect"):
            with self.subTest(suffix=suffix):
                self.align_helm("require (\n\thelm.sh/helm/v4 v4.3.0" + suffix + "\n)\n")

    def test_missing_and_duplicate_module_requirements_reject(self):
        for body in ("require (\n)\n", "require (\n\thelm.sh/helm/v4 v4.3.0\n\thelm.sh/helm/v4 v4.3.0 // indirect\n)\n"):
            with self.subTest(body=body):
                with self.assertRaisesRegex(ValueError, "missing/extra consumer"):
                    self.align_helm(body)

    def test_retired_major_and_module_prefix_cannot_satisfy_active_consumer(self):
        for module in ("helm.sh/helm/v3", "helm.sh/helm/v40", "example.com/helm.sh/helm/v4"):
            with self.subTest(module=module):
                with self.assertRaisesRegex(ValueError, "missing/extra consumer"):
                    self.align_helm("require (\n\t" + module + " v4.3.0\n)\n")

    def test_wrong_version_and_unrecognized_annotation_reject(self):
        for value in ("v4.3.1", "v4.3.00", "v4.3.0 // direct", "v4.3.0-malicious"):
            with self.subTest(value=value):
                with self.assertRaises(ValueError):
                    self.align_helm("require (\n\thelm.sh/helm/v4 " + value + "\n)\n")

    def test_current_direct_graph_has_one_matching_consumer_each(self):
        source = (ROOT / "go.mod").read_text()
        direct = {m.group(1): m.group(2) for m in re.finditer(r"^\s+([^\s]+) (v[^\s]+)$", source, re.MULTILINE)}
        components = {c["id"][6:]: c for c in self.components}
        self.assertEqual(set(components), set(direct))
        self.assertEqual(len(components), 63)
        for module, version in direct.items():
            self.assertEqual(components[module]["selected"], version)
        toolchain.align({"components": self.components}, ROOT)

    def test_retired_helm_authority_and_migration_exception_are_absent(self):
        self.assertNotIn("gomod:helm.sh/helm/v3", {c["id"] for c in self.components})
        self.assertNotIn("exception", self.helm)
        self.assertEqual(self.helm["selected"], "v4.3.0")

    def test_current_indirect_graph_remains_explicit_and_unmanaged(self):
        source = (ROOT / "go.mod").read_text()
        indirect = {m.group(1) for m in re.finditer(r"^\s+([^\s]+) v[^\s]+ // indirect$", source, re.MULTILINE)}
        self.assertEqual(len(indirect), 187)
        self.assertTrue(indirect.isdisjoint({c["id"][6:] for c in self.components}))
        self.assertIn("k8s.io/kubectl", indirect)

    def test_go_tool_authority_stays_present_and_separate(self):
        tooling = [c for c in self.document["components"]
                   if c["ecosystem"] == "gomod" and not c["id"].startswith("gomod:")]
        self.assertEqual({c["id"] for c in tooling}, {"govulncheck", "oapi-codegen", "oasdiff"})
        self.assertTrue({c["id"] for c in tooling}.isdisjoint({c["id"] for c in self.components}))
        toolchain.align({"components": tooling}, ROOT)

    def test_otlp_selection_has_owned_finite_patch_exception(self):
        component = next(c for c in self.components if c["id"] == "gomod:go.opentelemetry.io/proto/otlp")
        self.assertEqual(component["selected"], "v1.11.0")
        self.assertEqual(component["discovery"]["version"], "v1.11.1")
        self.assertEqual(component["exception"]["reviewBy"], "2026-10-16T00:00:00Z")
        self.assertTrue((ROOT / component["exception"]["evidence"]).is_file())


if __name__ == "__main__":
    unittest.main()
