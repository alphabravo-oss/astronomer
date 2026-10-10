import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
from types import SimpleNamespace
from urllib.parse import urlsplit

SPEC = importlib.util.spec_from_file_location("metrics_fence", Path(__file__).parents[1] / "metrics-postgres-qualification.py")
fence = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(fence)


class MetricsOwnershipTests(unittest.TestCase):
    def fixture(self, major):
        self.lane = fence.LANES[major]
        self.namespace = {"metadata": {"name": fence.NAMESPACE, "uid": fence.NAMESPACE_UID, "labels": {"astronomer.io/owner": fence.OWNER}}}
        self.cluster = {"metadata": {"name": self.lane["cluster"], "namespace": fence.NAMESPACE, "uid": self.lane["uid"], "labels": {"astronomer.io/owner": fence.OWNER}}, "status": {"readyInstances": 3, "instances": 3, "currentPrimary": "current-primary"}}
        self.primary = {"metadata": {"name": "current-primary", "namespace": fence.NAMESPACE, "uid": "current-primary-uid", "labels": {"cnpg.io/cluster": self.lane["cluster"], "cnpg.io/instanceRole": "primary"}, "ownerReferences": [{"uid": self.lane["uid"], "kind": "Cluster", "controller": True}]}, "status": {"conditions": [{"type": "Ready", "status": "True"}]}}
        self.services = {"items": [{"metadata": {"name": "owned-rw", "namespace": fence.NAMESPACE, "uid": "owned-service-uid"}, "spec": {"type": "NodePort", "selector": {"cnpg.io/cluster": self.lane["cluster"], "cnpg.io/instanceRole": "primary"}, "ports": [{"port": 5432, "nodePort": 30433}]}}]}
        self.nodes = {"items": [{"status": {"addresses": [{"type": "InternalIP", "address": "192.0.2.1"}]}}]}
        self.address = urlsplit("postgres://192.0.2.1:30433/" + self.lane["database"])

    def setUp(self):
        self.fixture("18")

    def check(self):
        return fence.fence(self.namespace, self.cluster, self.primary, self.services, self.nodes, self.address, self.lane)

    def test_both_lanes_pass_with_dynamic_current_primary(self):
        for major in fence.LANES:
            with self.subTest(major=major):
                self.fixture(major)
                self.assertEqual(self.check()["database"], self.lane["database"])
                self.assertEqual(self.check()["currentPrimary"], "current-primary")

    def test_namespace_uid_or_owner_mismatch_rejected(self):
        for field, value in [("uid", "foreign"), ("labels", {})]:
            original = copy.deepcopy(self.namespace)
            self.namespace["metadata"][field] = value
            with self.assertRaisesRegex(ValueError, "namespace ownership"):
                self.check()
            self.namespace = original

    def test_cluster_uid_or_owner_mismatch_rejected(self):
        for field, value in [("uid", fence.LANES["17"]["uid"]), ("labels", {})]:
            original = copy.deepcopy(self.cluster)
            self.cluster["metadata"][field] = value
            with self.assertRaisesRegex(ValueError, "cluster ownership"):
                self.check()
            self.cluster = original

    def test_former_primary_foreign_controller_or_replica_rejected(self):
        for mutate in [lambda: self.primary["metadata"].update(name="former-primary"), lambda: self.primary["metadata"]["ownerReferences"][0].update(uid="foreign"), lambda: self.primary["metadata"]["labels"].update({"cnpg.io/instanceRole": "replica"})]:
            original = copy.deepcopy(self.primary)
            mutate()
            with self.assertRaisesRegex(ValueError, "primary ownership"):
                self.check()
            self.primary = original

    def test_nonready_cluster_and_primary_rejected(self):
        self.cluster["status"]["readyInstances"] = 2
        with self.assertRaisesRegex(ValueError, "fully ready"):
            self.check()
        self.cluster["status"]["readyInstances"] = 3
        self.primary["status"]["conditions"][0]["status"] = "False"
        with self.assertRaisesRegex(ValueError, "primary is not ready"):
            self.check()

    def test_foreign_service_replica_and_duplicate_port_rejected(self):
        original = copy.deepcopy(self.services)
        self.services["items"][0]["spec"]["selector"]["cnpg.io/instanceRole"] = "replica"
        with self.assertRaisesRegex(ValueError, "unique owned primary"):
            self.check()
        self.services = original
        self.services["items"].append(copy.deepcopy(original["items"][0]))
        with self.assertRaisesRegex(ValueError, "unique owned primary"):
            self.check()

    def test_foreign_endpoint_host_rejected(self):
        self.address = urlsplit("postgres://192.0.2.2:30433/" + self.lane["database"])
        with self.assertRaisesRegex(ValueError, "owned cluster node"):
            self.check()

    def test_endpoints_bind_ready_current_primary_uid(self):
        report = self.check()
        endpoint = {"metadata": {"namespace": fence.NAMESPACE, "name": report["service"], "uid": "endpoint-uid"}, "subsets": [{"addresses": [{"targetRef": {"kind": "Pod", "namespace": fence.NAMESPACE, "name": report["currentPrimary"], "uid": report["currentPrimaryUID"]}}], "ports": [{"port": 5432, "protocol": "TCP"}]}]}
        self.assertEqual(fence.endpoints_fence(endpoint, report)["readyEndpointTargetUID"], report["currentPrimaryUID"])
        for mutate in [lambda x: x["metadata"].update(name="foreign-service"), lambda x: x["subsets"][0]["addresses"][0]["targetRef"].update(uid="former-primary"), lambda x: x["subsets"][0].update(notReadyAddresses=[{}]), lambda x: x["subsets"][0].update(addresses=[]), lambda x: x["subsets"][0]["ports"][0].update(port=5433)]:
            altered = copy.deepcopy(endpoint)
            mutate(altered)
            with self.assertRaises(ValueError):
                fence.endpoints_fence(altered, report)

    def test_reference_rejects_database_uid_fragment_override_and_duplicate(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "ref.json"
            valid = {"database_url": self.address.geturl(), "database": self.lane["database"], "cluster_uid": self.lane["uid"], "primary_at_allocation": "former-primary"}
            path.write_text(json.dumps(valid)); path.chmod(0o600)
            self.assertEqual(fence.reference(path, self.lane)[0], self.address)
            cases = [{**valid, "database": "shared"}, {**valid, "cluster_uid": "foreign"}, {**valid, "database_url": self.address.geturl() + "#fragment"}, {**valid, "database_url": self.address.geturl() + "?options=unsafe"}, {**valid, "database_url": self.address.geturl() + "?sslmode=require&sslmode=disable"}, {**valid, "database_url": self.address.geturl() + "?sslmode"}]
            for document in cases:
                path.write_text(json.dumps(document))
                with self.assertRaises(ValueError):
                    fence.reference(path, self.lane)

    def test_reference_requires_private_regular_file(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "ref.json"
            path.write_text("{}"); path.chmod(0o644)
            with self.assertRaisesRegex(ValueError, "private regular"):
                fence.reference(path, self.lane)
            link = Path(directory) / "link.json"; link.symlink_to(path)
            with self.assertRaisesRegex(ValueError, "private regular"):
                fence.reference(link, self.lane)

    def test_exact_parents_reject_missing_skipped_duplicate_unexpected_or_package_failure(self):
        passing = [{"Action": "pass", "Test": name} for name in sorted(fence.PARENTS)]
        mutations = [[], passing[:-1], passing + [passing[0]], passing + [{"Action": "pass", "Test": "TestFuture"}], [{**event, "Action": "skip"} if i == 0 else event for i, event in enumerate(passing)], passing + [{"Action": "fail", "Package": "server"}]]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "go.jsonl"
            for events in mutations:
                path.write_text("\n".join(json.dumps(event) for event in events))
                self.assertFalse(fence.parent_results(path)["qualified"])
            path.write_text("\n".join(json.dumps(event) for event in passing))
            self.assertTrue(fence.parent_results(path)["qualified"])

    def test_outer_timeout_terminates_owned_process_group(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "output"
            with path.open("w") as handle:
                with self.assertRaises(subprocess.TimeoutExpired):
                    fence.run_test_process([sys.executable, "-c", "import os,time;print(os.getpgrp(),flush=True);time.sleep(30)"], os.environ.copy(), handle, timeout=0.2)
            with self.assertRaises(ProcessLookupError):
                os.killpg(int(path.read_text().strip()), 0)

    def test_malformed_or_unstructured_event_fails_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "go.jsonl"
            for raw in ("not JSON", "[]", "{}"):
                path.write_text(raw)
                result = fence.parent_results(path)
                self.assertFalse(result["qualified"])
                self.assertEqual(result["error"], "invalid_json_event")


class MetricsFailureReceiptTests(unittest.TestCase):
    def exercise(self, process, *, ownership_error=False, final_guard_exit=0, changed_source=False):
        with tempfile.TemporaryDirectory() as directory:
            artifact = Path(directory)
            args = SimpleNamespace(major="18", reference="/private/ref.json", race=True)
            guards = []
            def capture(path):
                guards.append(path.name)
                path.write_text(json.dumps({"meets_minimum": path.name != "capacity-after.json" or final_guard_exit == 0}))
                return final_guard_exit if path.name == "capacity-after.json" else 0
            def git(command, **kwargs):
                return "source-commit" if "rev-parse" in command else b""
            source = {"sha256": "same"}
            identities = [source, {"sha256": "changed"} if changed_source else source]
            owned = {"privateReferenceSHA256": "hash"}
            with mock.patch.object(fence, "capacity_capture", side_effect=capture), mock.patch.object(fence, "source_identity", side_effect=identities), mock.patch.object(fence.subprocess, "check_output", side_effect=git), mock.patch.object(fence, "live_fence", side_effect=ValueError("private detail") if ownership_error else None, return_value=owned), mock.patch.object(fence, "run_test_process", side_effect=process):
                code = fence.run_lane(args, artifact)
            receipt = json.loads((artifact / "receipt.json").read_text())
            self.assertEqual(guards[-1], "capacity-after.json")
            self.assertIsNotNone(receipt["sourceAfter"])
            self.assertNotIn("private detail", (artifact / "receipt.json").read_text())
            return code, receipt

    @staticmethod
    def passing(command, env, output):
        for name in sorted(fence.PARENTS):
            output.write(json.dumps({"Action": "pass", "Test": name}) + "\n")
        return 0

    def test_nonzero_child_exit_retained_despite_final_guard_failure(self):
        def failure(command, env, output):
            output.write(json.dumps({"Action": "fail", "Package": "server"}) + "\n")
            return 2
        code, receipt = self.exercise(failure, final_guard_exit=1)
        self.assertEqual(code, 2)
        self.assertEqual(receipt["testProcessExitCode"], 2)
        self.assertFalse(receipt["parents"]["qualified"])

    def test_timeout_retains_final_source_and_capacity(self):
        def timeout(command, env, output):
            output.write(json.dumps({"Action": "run", "Test": sorted(fence.PARENTS)[0]}) + "\n")
            error = subprocess.TimeoutExpired(command, 180)
            error.process_exit_code = -15
            raise error
        code, receipt = self.exercise(timeout)
        self.assertEqual(code, 124)
        self.assertEqual(receipt["testProcessExitCode"], -15)
        self.assertEqual(receipt["errorKind"], "timeout")
        self.assertIsNotNone(receipt["logSHA256"])

    def test_ownership_exception_has_no_test_process_and_preserves_receipt(self):
        code, receipt = self.exercise(self.passing, ownership_error=True)
        self.assertEqual(code, 1)
        self.assertIsNone(receipt["testProcessExitCode"])
        self.assertIsNone(receipt["logSHA256"])
        self.assertEqual(receipt["stage"], "ownership")

    def test_successful_tests_fail_for_source_drift_or_final_guard_failure(self):
        for flags in [{"changed_source": True}, {"final_guard_exit": 1}]:
            with self.subTest(flags=flags):
                code, receipt = self.exercise(self.passing, **flags)
                self.assertEqual(code, 1)
                self.assertEqual(receipt["testProcessExitCode"], 0)
                self.assertTrue(receipt["parents"]["qualified"])

    def test_missing_parents_never_pass_on_zero_child_exit(self):
        code, receipt = self.exercise(lambda command, env, output: 0)
        self.assertEqual(code, 1)
        self.assertFalse(receipt["parents"]["qualified"])


if __name__ == "__main__":
    unittest.main()
