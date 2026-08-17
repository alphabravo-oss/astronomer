#!/usr/bin/env python3
from __future__ import annotations

import copy
import importlib.util
import json
from pathlib import Path
import stat
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts/compatibility-contract.py"
SPEC = importlib.util.spec_from_file_location("compatibility_contract", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class CompatibilityContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.contract = MODULE.load_json(ROOT / "deploy/release/compatibility.yaml")
        cls.schema = MODULE.load_json(ROOT / "deploy/release/compatibility.schema.json")

    def test_repository_contract_is_valid_and_documentation_is_deterministic(self) -> None:
        contract = MODULE.validate(
            ROOT / "deploy/release/compatibility.yaml",
            ROOT / "deploy/release/compatibility.schema.json",
            ROOT,
        )
        generated = MODULE.generate_doc(contract)
        self.assertEqual(generated, (ROOT / "docs/architecture/compatibility.md").read_text(encoding="utf-8"))

    def test_schema_rejects_unknown_fields(self) -> None:
        invalid = copy.deepcopy(self.contract)
        invalid["legacy_provider"] = "argo"
        with self.assertRaisesRegex(MODULE.ContractError, "unexpected keys: legacy_provider"):
            MODULE.validate_schema(invalid, self.schema, self.schema)

    def test_semantics_reject_empty_and_overlapping_ranges(self) -> None:
        empty = copy.deepcopy(self.contract)
        empty["kubernetes"]["supported_ranges"] = [
            {"minimum_minor": "1.35", "maximum_minor": "1.33"}
        ]
        with self.assertRaisesRegex(MODULE.ContractError, "empty range"):
            MODULE.validate_semantics(empty, ROOT)

        overlap = copy.deepcopy(self.contract)
        overlap["agent_protocol"]["supported_ranges"] = [
            {"minimum": 1, "maximum": 2},
            {"minimum": 2, "maximum": 3},
        ]
        with self.assertRaisesRegex(MODULE.ContractError, "overlaps range"):
            MODULE.validate_semantics(overlap, ROOT)

    def test_every_advertised_minor_requires_an_executable_live_or_ci_runner(self) -> None:
        invalid = copy.deepcopy(self.contract)
        invalid["kubernetes"]["qualification"] = invalid["kubernetes"]["qualification"][:-1]
        with self.assertRaisesRegex(MODULE.ContractError, "qualification mismatch"):
            MODULE.validate_semantics(invalid, ROOT)

        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            runner = root / "matrix.sh"
            runner.write_text("#!/bin/sh\n", encoding="utf-8")
            runner.chmod(runner.stat().st_mode | stat.S_IXUSR)
            minimal = copy.deepcopy(self.contract)
            minimal["kubernetes"]["supported_ranges"] = [
                {"minimum_minor": "1.33", "maximum_minor": "1.33"}
            ]
            minimal["kubernetes"]["advertised_minors"] = ["1.33"]
            minimal["kubernetes"]["qualification"] = [
                {
                    "minor": "1.33",
                    "kind": "live",
                    "runner": "matrix.sh",
                    "command": "./matrix.sh v1.34.0",
                }
            ]
            with self.assertRaisesRegex(MODULE.ContractError, "does not select Kubernetes 1.33"):
                MODULE.validate_semantics(minimal, root)


if __name__ == "__main__":
    unittest.main()
