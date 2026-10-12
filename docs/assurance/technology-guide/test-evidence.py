"""Reject false passing claims and changed source/artifact bindings."""

import copy
import importlib.util
import pathlib
import tempfile
import unittest

import jsonschema


HERE = pathlib.Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("guide_evidence", HERE / "validate-evidence.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class EvidenceContractTest(unittest.TestCase):
    def setUp(self):
        self.baseline = MODULE.load(HERE / "baseline.json")
        self.components = MODULE.load(HERE / "components.json")
        self.records = [copy.deepcopy(next(r for r in MODULE.load(HERE / "verification.json")
                                          if r["result"]["status"] == "pass"))]

    def check(self):
        MODULE.validate_records(self.records, self.baseline, self.components)

    def test_retained_passing_record(self):
        self.check()

    def test_unknown_fields_and_invalid_source_sha(self):
        self.records[0]["secretValue"] = "synthetic-unrecognized-field"
        with self.assertRaises(jsonschema.ValidationError):
            self.check()
        self.records[0].pop("secretValue")
        self.records[0]["source"]["applicationCommit"] = "not-a-commit"
        with self.assertRaises(jsonschema.ValidationError):
            self.check()

    def test_changed_source_identity(self):
        self.records[0]["source"]["applicationCommit"] = "0" * 40
        with self.assertRaisesRegex(ValueError, "record source"):
            self.check()

    def test_changed_candidate_tree_digest(self):
        self.records[0]["source"]["candidateSourceTreeDigest"] = "sha256:" + "0" * 64
        with self.assertRaisesRegex(ValueError, "tree digest"):
            self.check()

    def test_other_package_static_record_needs_no_manifest(self):
        self.records[0]["workPackage"] = "W05"
        self.check()

    def test_candidate_qualification_requires_manifest(self):
        self.records[0]["environment"]["kind"] = "candidate-qualification"
        with self.assertRaisesRegex(ValueError, "manifest binding"):
            self.check()

    def test_pass_cannot_hide_failure_skip_or_missing_cases(self):
        for field, value in [("exitCode", 1), ("unexpectedSkips", 1), ("executedCases", 99999)]:
            with self.subTest(field=field):
                old = self.records[0]["execution"][field]
                self.records[0]["execution"][field] = value
                with self.assertRaises(ValueError):
                    self.check()
                self.records[0]["execution"][field] = old

    def test_reversed_timestamps(self):
        self.records[0]["execution"]["finishedAt"] = "2000-01-01T00:00:00Z"
        with self.assertRaisesRegex(ValueError, "reversed"):
            self.check()

    def test_changed_artifact_and_escape(self):
        artifact = self.records[0]["result"]["artifacts"][0]
        artifact["digest"] = "sha256:" + "0" * 64
        with self.assertRaisesRegex(ValueError, "artifact digest"):
            self.check()
        artifact["path"] = "../../outside-evidence"
        with self.assertRaisesRegex(ValueError, "within the repository"):
            self.check()

    def test_symlink_artifact_is_rejected(self):
        artifact = self.records[0]["result"]["artifacts"][0]
        target = MODULE.ROOT / artifact["path"]
        with tempfile.TemporaryDirectory(dir=HERE) as directory:
            link = pathlib.Path(directory) / "linked.log"
            link.symlink_to(target)
            artifact["path"] = str(link.relative_to(MODULE.ROOT))
            with self.assertRaisesRegex(ValueError, "regular file"):
                self.check()

    def test_deferral_requires_authority_and_reason(self):
        record = self.records[0]
        record["result"]["status"] = "deferred"
        record["execution"]["executedCases"] = 0
        record["execution"]["exitCode"] = None
        with self.assertRaisesRegex(ValueError, "reason"):
            self.check()
        record["result"]["reason"] = "fixture-only reason"
        with self.assertRaisesRegex(ValueError, "authority"):
            self.check()

    def test_duplicate_record(self):
        self.records.append(copy.deepcopy(self.records[0]))
        with self.assertRaisesRegex(ValueError, "duplicate"):
            self.check()

    def test_changed_inventory_input_digest(self):
        self.components["sourceInputs"][0]["digest"] = "sha256:" + "0" * 64
        with self.assertRaisesRegex(ValueError, "source input digest"):
            MODULE.validate_source_inputs(self.baseline, self.components)


if __name__ == "__main__":
    unittest.main()
