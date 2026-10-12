import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from urllib.parse import urlsplit

SPEC = importlib.util.spec_from_file_location("rotation_fence", Path(__file__).parents[1] / "rotation-postgres-qualification.py")
fence = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(fence)


class RotationOwnershipTests(unittest.TestCase):
    def setUp(self):
        self.cluster = {"metadata": {"name": fence.CLUSTER, "namespace": fence.NAMESPACE, "uid": fence.CLUSTER_UID, "labels": {"astronomer.io/owner": fence.OWNER}}, "status": {"readyInstances": 3, "currentPrimary": "current-primary"}}
        self.primary = {"metadata": {"name": "current-primary", "namespace": fence.NAMESPACE, "uid": "current-primary-uid", "labels": {"cnpg.io/cluster": fence.CLUSTER, "cnpg.io/instanceRole": "primary"}, "ownerReferences": [{"uid": fence.CLUSTER_UID, "kind": "Cluster", "controller": True}]}, "status": {"conditions": [{"type": "Ready", "status": "True"}]}}
        self.services = {"items": [{"metadata": {"name": "owned-rw", "namespace": fence.NAMESPACE, "uid": "owned-rw-uid"}, "spec": {"type": "NodePort", "selector": {"cnpg.io/cluster": fence.CLUSTER, "cnpg.io/instanceRole": "primary"}, "ports": [{"port": 5432, "nodePort": 30433}]}}]}
        self.nodes = {"items": [{"status": {"addresses": [{"type": "InternalIP", "address": "192.0.2.1"}]}}]}
        self.address = urlsplit("postgres://192.0.2.1:30433/plan032_w16_rotation")

    def check(self):
        return fence.fence(self.cluster, self.primary, self.services, self.nodes, self.address)

    def test_live_owned_endpoint_passes_with_dynamic_primary(self):
        self.assertEqual(self.check()["currentPrimaryUID"], "current-primary-uid")

    def test_cluster_uid_and_owner_mismatch_rejected(self):
        for field in ("uid", "labels"):
            with self.subTest(field=field):
                original = copy.deepcopy(self.cluster)
                self.cluster["metadata"][field] = "other" if field == "uid" else {"astronomer.io/owner": "other"}
                with self.assertRaisesRegex(ValueError, "cluster ownership"):
                    self.check()
                self.cluster = original

    def test_former_primary_and_foreign_controller_rejected(self):
        for change in (lambda: self.primary["metadata"].update(name="former-primary"), lambda: self.primary["metadata"]["ownerReferences"][0].update(uid="other")):
            original = copy.deepcopy(self.primary)
            change()
            with self.assertRaisesRegex(ValueError, "primary owner"):
                self.check()
            self.primary = original

    def test_not_ready_primary_or_cluster_rejected(self):
        self.primary["status"]["conditions"][0]["status"] = "False"
        with self.assertRaisesRegex(ValueError, "primary is not ready"):
            self.check()
        self.primary["status"]["conditions"][0]["status"] = "True"
        self.cluster["status"]["readyInstances"] = 2
        with self.assertRaisesRegex(ValueError, "cluster is not fully ready"):
            self.check()

    def test_foreign_or_replica_service_rejected(self):
        self.services["items"][0]["spec"]["selector"]["cnpg.io/instanceRole"] = "replica"
        with self.assertRaisesRegex(ValueError, "owned primary service"):
            self.check()

    def test_duplicate_port_service_rejected(self):
        self.services["items"].append(copy.deepcopy(self.services["items"][0]))
        with self.assertRaisesRegex(ValueError, "owned primary service"):
            self.check()

    def test_foreign_endpoint_host_rejected(self):
        self.address = urlsplit("postgres://192.0.2.2:30433/plan032_w16_rotation")
        with self.assertRaisesRegex(ValueError, "owned cluster node"):
            self.check()

    def test_zero_skipped_missing_duplicate_or_unexpected_parents_rejected(self):
        passing = [{"Action": "pass", "Test": name} for name in sorted(fence.PARENTS)]
        mutations = [[], passing[:-1], passing + [passing[0]], passing + [{"Action": "pass", "Test": "TestPostgresRotationFuture"}], [{**event, "Action": "skip"} if i == 0 else event for i, event in enumerate(passing)]]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "go.jsonl"
            for events in mutations:
                path.write_text("\n".join(json.dumps(event) for event in events))
                self.assertFalse(fence.parent_results(path)["qualified"])
            path.write_text("\n".join(json.dumps(event) for event in passing))
            self.assertTrue(fence.parent_results(path)["qualified"])

    def test_outer_timeout_terminates_owned_process_group(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "output"
            with output.open("w") as handle:
                with self.assertRaises(subprocess.TimeoutExpired):
                    fence.run_test_process([sys.executable, "-c", "import os,time;print(os.getpgrp(),flush=True);time.sleep(30)"], os.environ.copy(), handle, timeout=0.2)
            group = int(output.read_text().strip())
            with self.assertRaises(ProcessLookupError):
                os.killpg(group, 0)


if __name__ == "__main__":
    unittest.main()
