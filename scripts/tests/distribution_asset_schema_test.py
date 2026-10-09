#!/usr/bin/env python3
"""Closed external prerequisite input contract, without supplier qualification."""
import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("asset_contract_validator", ROOT / "scripts/compatibility-contract.py")
VALIDATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VALIDATOR)
SCHEMA = json.loads((ROOT / "deploy/bundles/schemas/distribution-asset-input-v2.schema.json").read_bytes())


class DistributionAssetSchemaTest(unittest.TestCase):
    def document(self):
        return {"schema_version": 2, "required_categories": ["catalog", "chart", "values", "image_inventory"],
                "capabilities": ["database-cnpg"], "assets": [{"id": "postgres-server", "path": "assets/cluster.yaml",
                "file": "/reviewed/cluster.yaml", "category": "database", "images": ["registry.example.test/postgres@sha256:" + "a" * 64],
                "digest": "sha256:" + "b" * 64, "artifact_subject": "", "artifact_manifest_file": ""}]}

    def validate(self, value):
        VALIDATOR.validate_schema(value, SCHEMA, SCHEMA)

    def test_valid_external_shape(self):
        self.validate(self.document())  # semantic reader still requires every CNPG capability role

    def test_unknown_missing_and_unclassified_fields(self):
        for mutation in (lambda doc: doc.update(allow_unqualified=True), lambda doc: doc.pop("capabilities"),
                         lambda doc: doc["assets"][0].pop("digest"), lambda doc: doc["assets"][0].update(id="any-operator"),
                         lambda doc: doc.update(capabilities=["database-cnpg", "database-cnpg"]),
                         lambda doc: doc["assets"][0].update(images=["registry.example.test/postgres:latest"])):
            document = self.document()
            mutation(document)
            with self.assertRaises(VALIDATOR.ContractError):
                self.validate(document)


if __name__ == "__main__":
    unittest.main()
