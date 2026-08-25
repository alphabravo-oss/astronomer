import importlib.util
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("cloud_acceptance", ROOT / "scripts" / "validate-cloud-acceptance.py")
cloud = importlib.util.module_from_spec(SPEC); SPEC.loader.exec_module(cloud)


class CloudAcceptanceTest(unittest.TestCase):
    def test_replay_requires_exact_same_durable_receipt(self):
        cluster = "00000000-0000-4000-8000-000000000001"
        receipt = {"cluster_id": cluster, "status": "accepted"}
        self.assertEqual(cloud.validate_replay_receipts(receipt, dict(receipt), cluster), receipt)
        with self.assertRaisesRegex(RuntimeError, "exact durable"):
            cloud.validate_replay_receipts(receipt, {**receipt, "operation_id": "duplicate"}, cluster)
        with self.assertRaisesRegex(RuntimeError, "exact durable"):
            cloud.validate_replay_receipts(receipt, {"cluster_id": cluster, "status": "queued"}, cluster)


if __name__ == "__main__":
    unittest.main()
