#!/usr/bin/env python3
"""Offline coverage checks for the Plan 028 qualification denominator."""

import json
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
MATRIX = ROOT / "advisor-plans/028-offering-test-inventory.md"
MANIFEST = ROOT / "scripts/testdata/offering-qualification/cases.json"
SOURCE = ROOT / "advisor-plans/028-offering-source-inventory.json"
SCHEMA = ROOT / "deploy/release/offering-qualification.schema.json"
RUNNER = ROOT / "scripts/qualify-offerings/main.go"
WORKFLOW = ROOT / ".github/workflows/offering-api-qualification.yaml"


class OfferingQualificationContractTest(unittest.TestCase):
    def setUp(self):
        self.manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))

    def test_all_documented_cases_are_in_the_machine_denominator(self):
        documented = re.findall(
            r"^\|\s*((?:APP|TOOL|BASE|ID|OBS|LOG|SEND|SIEM|SECRET|CLOUD|SOURCE|DR|POLICY|CIS|MESH|SEC|EXT|AI|PLATFORM)-\d+)\s*\|",
            MATRIX.read_text(encoding="utf-8"),
            re.MULTILINE,
        )
        configured = [case["id"] for case in self.manifest["cases"]]
        self.assertEqual(174, len(documented))
        self.assertEqual(len(documented), len(set(documented)))
        self.assertEqual(documented, configured)
        self.assertTrue(all(case["required"] is True for case in self.manifest["cases"]))

    def test_catalog_and_tool_source_snapshots_match_runtime_contracts(self):
        source = json.loads(SOURCE.read_text(encoding="utf-8"))
        registries = {row["id"]: row for row in self.manifest["runtime_registries"]}
        catalog = sorted(app["slug"] for app in source["catalogApplications"])
        tools = sorted(source["seededToolSlugs"])
        self.assertEqual(catalog, sorted(registries["catalog-applications"]["expected_identities"]))
        self.assertEqual(tools, sorted(registries["tools"]["expected_identities"]))

    def test_registry_contracts_are_unique_and_api_scoped(self):
        registries = self.manifest["runtime_registries"]
        ids = [row["id"] for row in registries]
        self.assertEqual(len(ids), len(set(ids)))
        for registry in registries:
            self.assertTrue(registry["path"].startswith("/api/v1/"))
            expected = registry["expected_identities"]
            self.assertEqual(sorted(set(expected)), sorted(expected))

    def test_evidence_schema_is_closed_at_every_evidence_object(self):
        schema = json.loads(SCHEMA.read_text(encoding="utf-8"))
        self.assertFalse(schema["additionalProperties"])
        for name in ("candidate", "target", "registry_result", "case_result", "summary", "cleanup"):
            self.assertFalse(schema["$defs"][name]["additionalProperties"], name)
        self.assertEqual("astronomer-offering-qualification-v1", schema["properties"]["schema_version"]["const"])

    def test_inventory_runner_has_no_mutating_http_method(self):
        runner = RUNNER.read_text(encoding="utf-8")
        self.assertIn("http.MethodGet", runner)
        for method in ("MethodPost", "MethodPut", "MethodPatch", "MethodDelete"):
            self.assertNotIn("http." + method, runner)
        self.assertNotIn("InsecureSkipVerify", runner)

    def test_manual_workflow_is_protected_and_retains_only_evidence(self):
        workflow = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("environment: release-candidate", workflow)
        self.assertIn("OFFERING_QUALIFICATION_API_TOKEN", workflow)
        self.assertIn("go run ./scripts/qualify-offerings inventory", workflow)
        self.assertIn("rm -f \"$RUNNER_TEMP/offering-token\"", workflow)
        for bypass in ("kubectl", "helm install", "curl -k", "insecure-skip"):
            self.assertNotIn(bypass, workflow.lower())


if __name__ == "__main__":
    unittest.main()
