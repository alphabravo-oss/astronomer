import copy
import importlib.util
import os
from pathlib import Path
import sys
import tempfile
import unittest
from urllib.parse import urlsplit

spec = importlib.util.spec_from_file_location('cost_runner', Path(__file__).with_name('run-bounded.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)

class RuntimeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.log = Path(self.temp.name) / 'test.jsonl'
        self.stages = []
    def guard(self, stage): self.stages.append(stage)
    def execute(self, code, guard=None, ownership=lambda: None, timeout=2, identity=lambda: {'digest': 'fixed'}):
        return runner.run_lane([sys.executable, '-c', code], os.environ.copy(), self.log, guard or self.guard, ownership, identity, timeout=timeout, interval=.03, grace=.2)
    def test_normal_exit_retains_fingerprint_and_final_guard(self):
        receipt = self.execute("print('public synthetic event')")
        self.assertEqual(receipt['exit'], 0)
        self.assertTrue(receipt['source_stable'] and receipt['owned_group_joined'] and receipt['final_guard_pass'])
        self.assertEqual(self.stages[-1], 'final')
    def test_timeout_joins_owned_child_and_descendant(self):
        receipt = self.execute("import subprocess,sys,time;subprocess.Popen([sys.executable,'-c','import time;time.sleep(30)']);time.sleep(30)", timeout=.15)
        self.assertEqual(receipt['failure_category'], 'owned_process_timeout')
        self.assertTrue(receipt['owned_group_joined'] and receipt['final_guard_pass'])
    def test_maintained_guard_failure_keeps_partial_log_and_child_exit(self):
        def guard(stage):
            self.stages.append(stage)
            if stage == 'maintained' and self.log.stat().st_size: raise AssertionError()
        receipt = self.execute("import time;print('partial synthetic event',flush=True);time.sleep(30)", guard=guard)
        self.assertEqual(receipt['failure_category'], 'AssertionError')
        self.assertLess(receipt['exit'], 0)
        self.assertIn('partial synthetic event', self.log.read_text())
        self.assertTrue(receipt['owned_group_joined'] and receipt['final_guard_pass'])
    def test_ownership_failure_prevents_child_and_still_checks_final_guard(self):
        def reject(): raise ValueError()
        receipt = self.execute("print('must not execute')", ownership=reject)
        self.assertIsNone(receipt['exit'])
        self.assertFalse(self.log.exists())
        self.assertEqual(receipt['failure_category'], 'ValueError')
        self.assertTrue(receipt['final_guard_pass'])
    def test_preflight_guard_failure_still_checks_final_guard(self):
        def guard(stage):
            self.stages.append(stage)
            if stage == 'preflight': raise AssertionError()
        receipt = self.execute("print('must not execute')", guard=guard)
        self.assertFalse(self.log.exists())
        self.assertEqual(self.stages, ['preflight', 'final'])
        self.assertTrue(receipt['final_guard_pass'])
    def test_final_guard_red_does_not_replace_original_child_exit(self):
        def guard(stage):
            self.stages.append(stage)
            if stage == 'final': raise AssertionError()
        receipt = self.execute('raise SystemExit(7)', guard=guard)
        self.assertEqual(receipt['exit'], 7)
        self.assertIsNone(receipt['failure_category'])
        self.assertFalse(receipt['final_guard_pass'])
    def test_interruption_joins_partial_child_and_keeps_final_receipt(self):
        def guard(stage):
            self.stages.append(stage)
            if stage == 'maintained' and self.log.stat().st_size: raise KeyboardInterrupt()
        receipt = self.execute("import time;print('partial synthetic event',flush=True);time.sleep(30)", guard=guard)
        self.assertEqual(receipt['failure_category'], 'KeyboardInterrupt')
        self.assertTrue(receipt['owned_group_joined'] and receipt['final_guard_pass'])
    def test_failed_after_fingerprint_preserves_original_exit_and_final_guard(self):
        calls = 0
        def identity():
            nonlocal calls
            calls += 1
            if calls == 2: raise OSError()
            return {'digest': 'fixed'}
        receipt = self.execute('raise SystemExit(7)', identity=identity)
        self.assertEqual(receipt['exit'], 7)
        self.assertEqual(receipt['source_after_error'], 'OSError')
        self.assertFalse(receipt['source_stable'])
        self.assertTrue(receipt['final_guard_pass'])
    def test_source_mutation_is_detected_even_with_clean_git_claim(self):
        identities = iter([{'digest': 'before'}, {'digest': 'changed'}])
        receipt = self.execute('pass', identity=lambda: next(identities))
        self.assertFalse(receipt['source_stable'])

class SchemaTests(unittest.TestCase):
    def test_actual_frozen_typed_declaration_matches_75(self):
        self.assertEqual(runner.expected_schema_version(runner.ROOT), 75)
    def test_missing_comment_wrong_type_or_duplicate_declaration_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            target = root / 'internal/db/schema_version.go'
            target.parent.mkdir(parents=True)
            for source in ['// const ExpectedSchemaVersion int64 = 75\n', 'const ExpectedSchemaVersion = 75\n', 'const ExpectedSchemaVersion string = "75"\n', 'const ExpectedSchemaVersion int64 = 75\nconst ExpectedSchemaVersion int64 = 77\n']:
                target.write_text(source)
                with self.assertRaises(AssertionError): runner.expected_schema_version(root)
            target.write_text('const ExpectedSchemaVersion int64 = 77\n')
            self.assertEqual(runner.expected_schema_version(root), 77)

class OwnershipTests(unittest.TestCase):
    def setUp(self):
        name, uid, service_uid, port = runner.CLUSTERS[17]
        self.address = urlsplit('postgres://192.0.2.1:'+str(port)+'/plan032_w07_cursor17')
        self.ns = {'metadata': {'name': runner.NAMESPACE, 'uid': runner.NAMESPACE_UID, 'labels': {'astronomer.io/owner': runner.OWNER}}, 'status': {'phase': 'Active'}}
        self.cluster = {'metadata': {'name': name, 'uid': uid, 'namespace': runner.NAMESPACE, 'labels': {'astronomer.io/owner': runner.OWNER}}, 'status': {'readyInstances': 3, 'currentPrimary': 'primary'}}
        self.pod = {'metadata': {'name': 'primary', 'uid': 'pod-uid', 'namespace': runner.NAMESPACE, 'labels': {'cnpg.io/cluster': name, 'cnpg.io/instanceRole': 'primary'}, 'ownerReferences': [{'controller': True, 'kind': 'Cluster', 'uid': uid, 'apiVersion': 'postgresql.cnpg.io/v1'}]}, 'status': {'podIP': '192.0.2.2', 'conditions': [{'type': 'Ready', 'status': 'True'}]}}
        self.services = {'items': [{'metadata': {'namespace': runner.NAMESPACE, 'name': 'service', 'uid': service_uid}, 'spec': {'type': 'NodePort', 'selector': {'cnpg.io/cluster': name, 'cnpg.io/instanceRole': 'primary'}, 'ports': [{'nodePort': port, 'port': 5432, 'targetPort': 5432}]}}]}
        self.endpoints = {'metadata': {'namespace': runner.NAMESPACE, 'name': 'service'}, 'subsets': [{'addresses': [{'nodeName': 'node', 'ip': '192.0.2.2', 'targetRef': {'kind': 'Pod', 'namespace': runner.NAMESPACE, 'name': 'primary', 'uid': 'pod-uid'}}], 'ports': [{'port': 5432}]}]}
        self.node = {'metadata': {'name': 'node', 'uid': 'node-uid'}, 'status': {'addresses': [{'type': 'InternalIP', 'address': '192.0.2.1'}]}}
    def check(self): return runner.check_ownership(17, self.address, self.ns, self.cluster, self.pod, self.services, self.endpoints, self.node)
    def test_exact_owned_ready_endpoint_passes(self): self.assertEqual(self.check()['ready_instances'], 3)
    def test_namespace_uid_owner_and_cluster_readiness_rejected(self):
        for change in [lambda: self.ns['metadata'].update(uid='foreign'), lambda: self.ns['metadata']['labels'].update({'astronomer.io/owner': 'foreign'}), lambda: self.cluster['status'].update(readyInstances=2)]:
            before = copy.deepcopy((self.ns,self.cluster));change()
            with self.assertRaises(AssertionError): self.check()
            self.ns,self.cluster = before
    def test_primary_controller_role_and_readiness_rejected(self):
        for change in [lambda: self.pod['metadata']['ownerReferences'][0].update(uid='foreign'), lambda: self.pod['metadata']['labels'].update({'cnpg.io/instanceRole':'replica'}), lambda: self.pod['status']['conditions'][0].update(status='False')]:
            before=copy.deepcopy(self.pod);change()
            with self.assertRaises(AssertionError):self.check()
            self.pod=before
    def test_notready_endpoint_wrong_port_and_externalip_rejected(self):
        for change in [lambda: self.endpoints['subsets'][0].update(notReadyAddresses=[{}]), lambda: self.endpoints['subsets'][0]['ports'][0].update(port=5433), lambda: self.node['status']['addresses'][0].update(type='ExternalIP'), lambda: self.services['items'][0]['spec'].update(type='ClusterIP')]:
            before=copy.deepcopy((self.endpoints,self.node,self.services));change()
            with self.assertRaises(AssertionError):self.check()
            self.endpoints,self.node,self.services=before
    def test_private_reference_is_atomic0600_and_cannot_overwrite_or_follow_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            parent=Path(directory);source='a'*40
            reference=Path(runner.private_write(source,17,'synthetic-private-url',root=parent))
            self.assertEqual(reference.stat().st_mode&0o777,0o600)
            self.assertEqual(reference.parent.stat().st_mode&0o777,0o700)
            with self.assertRaises(FileExistsError):runner.private_write(source,17,'replacement',root=parent)
            reference.unlink();reference.symlink_to(parent/'foreign')
            with self.assertRaises(FileExistsError):runner.private_write(source,17,'replacement',root=parent)
    def test_private_parent_symlink_is_rejected_before_any_file_creation(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);foreign=root/'foreign';foreign.mkdir(mode=0o700)
            (root/('w07-cost-'+'b'*8)).symlink_to(foreign,target_is_directory=True)
            with self.assertRaises(OSError):runner.private_write('b'*40,17,'synthetic-private-url',root=root)
            self.assertEqual(list(foreign.iterdir()),[])

if __name__=='__main__':unittest.main()
