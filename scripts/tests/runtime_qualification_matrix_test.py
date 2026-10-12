import copy
import importlib.util
import json
from pathlib import Path
import unittest
import datetime as dt

spec = importlib.util.spec_from_file_location("matrix", Path(__file__).parents[1] / "runtime-qualification-matrix.py")
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class MatrixTests(unittest.TestCase):
    def setUp(self):
        self.doc = json.loads(m.MATRIX.read_text())
        self.now = dt.datetime(2026, 10, 9, 23, tzinfo=dt.timezone.utc)

    def test_current_matrix(self):
        m.validate(self.doc, now=self.now)

    def test_expired_review(self):
        with self.assertRaisesRegex(ValueError, "expired"):
            m.validate(self.doc, now=dt.datetime(2026, 10, 16, tzinfo=dt.timezone.utc))

    def test_future_discovery(self):
        self.doc["observedAt"] = "2027-01-01T00:00:00Z"
        with self.assertRaisesRegex(ValueError, "future"):
            m.validate(self.doc, now=self.now)

    def test_naive_discovery(self):
        self.doc["observedAt"] = "2026-10-09T00:00:00"
        with self.assertRaisesRegex(ValueError, "timezone"):
            m.validate(self.doc, now=self.now)

    def test_duplicate_claim(self):
        self.doc["claims"].append(copy.deepcopy(self.doc["claims"][0]))
        with self.assertRaisesRegex(ValueError, "duplicate"):
            m.validate(self.doc, now=self.now)

    def test_changed_offering_denominator(self):
        self.doc["offeringInventory"]["sha256"] = "0" * 64
        with self.assertRaisesRegex(ValueError, "denominator"):
            m.validate(self.doc, now=self.now)

    def test_external_pass_cannot_be_invented(self):
        self.doc["claims"][0]["classification"] = "externally_qualified"
        with self.assertRaisesRegex(ValueError, "external"):
            m.validate(self.doc, now=self.now)

    def test_unowned_lane_rejected(self):
        self.doc["lanes"].append("delete-shared-cluster")
        with self.assertRaisesRegex(ValueError, "lane"):
            m.validate(self.doc, now=self.now)

    def test_source_traversal_rejected(self):
        self.doc["claims"][0]["source"] = "../credentials"
        with self.assertRaisesRegex(ValueError, "source"):
            m.validate(self.doc, now=self.now)

    def test_missing_owner(self):
        self.doc["claims"][0]["owner"] = " "
        with self.assertRaisesRegex(ValueError, "owner"):
            m.validate(self.doc, now=self.now)

    def test_changed_local_evidence_rejected(self):
        self.doc["claims"][1]["evidence"][0]["sha256"] = "0" * 64
        with self.assertRaisesRegex(ValueError, "evidence bytes"):
            m.validate(self.doc, now=self.now)

    def test_missing_local_evidence_rejected(self):
        self.doc["claims"][1]["evidence"] = []
        with self.assertRaisesRegex(ValueError, "attributable"):
            m.validate(self.doc, now=self.now)

    def test_all_stateful_race_variants_bind_actual_script_switches(self):
        for name, key in {
            "process-restart": "PROCESS_RESTART_QUALIFICATION_RACE",
            "redis-outage": "REDIS_OUTAGE_RECOVERY_RACE",
            "postgres-outage": "POSTGRES_OUTAGE_QUALIFICATION_RACE",
            "streaming": "POSTGRES_FAILOVER_CERTIFICATION_RACE",
        }.items():
            for major in ("17", "18"):
                command, env = m.commands()[name + major + "-race"]
                self.assertEqual(env, {"POSTGRES_QUALIFICATION_MAJOR": major, key: "1"})
                self.assertIn(key, (m.ROOT / command[1]).read_text())

    def test_no_commands_come_from_mutable_document(self):
        command, env = m.commands()["postgres18-race"]
        self.assertEqual(command, ["bash", "scripts/test-postgres-integration.sh"])
        self.assertEqual(env, {"POSTGRES_QUALIFICATION_MAJOR": "18", "POSTGRES_INTEGRATION_RACE": "1"})


if __name__ == "__main__":
    unittest.main()
