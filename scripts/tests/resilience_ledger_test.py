"""Hermetic mutation/receipt/recovery tests; never invokes a cluster."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

SCRIPTS = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS))
spec = importlib.util.spec_from_file_location('resilience_runner', SCRIPTS / 'run-delivery-resilience-drill.py')
runner = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = runner
spec.loader.exec_module(runner)
from resilience_ledger import Ledger, LedgerError, OWNER, DISPOSABLE, NONCE, RESTART, RESOURCES


class Crash(BaseException):
    pass


class FakeKubectl:
    def __init__(self, namespace):
        self.namespace = namespace
        self.objects = {}
        self.calls = []
        self.hook = None
        self.get_hook = None
        self.revision = 0
        self.put('namespace', namespace, namespace=None)
        self.put('namespace', 'kube-system', namespace=None)
        self.put('deployment', 'agent', spec={'replicas': 2, 'template': {'metadata': {}}})

    def put(self, kind, name, **fields):
        self.revision += 1
        api, gvk, _ = RESOURCES.get(kind, ('v1', 'Namespace', 'namespaces'))
        namespace = fields.pop('namespace', self.namespace)
        obj = {'apiVersion': api, 'kind': gvk, 'metadata': {
            'name': name, 'namespace': namespace, 'uid': 'uid-' + str(self.revision),
            'resourceVersion': str(self.revision), 'labels': {OWNER: 'unit', DISPOSABLE: 'true'}}, **fields}
        self.objects[kind, name] = obj
        return obj

    def get(self, kind, name):
        result = self.get_optional(kind, name)
        if result is None:
            raise RuntimeError('missing')
        return result

    def get_optional(self, kind, name):
        if self.get_hook:
            self.get_hook(kind, name)
        return copy.deepcopy(self.objects.get((kind, name)))

    def run(self, args, stdin=None, timeout=60):
        self.calls.append((args, copy.deepcopy(stdin)))
        if self.hook:
            self.hook('before', args, stdin)
        if args[0] == 'create':
            kind = next(k for k, v in RESOURCES.items() if v[1] == stdin['kind'])
            name = stdin['metadata']['name']
            if (kind, name) in self.objects:
                raise RuntimeError('exists')
            obj = self.put(kind, name)
            uid, rv = obj['metadata']['uid'], obj['metadata']['resourceVersion']
            obj.update(copy.deepcopy(stdin))
            obj['metadata'].update(uid=uid, resourceVersion=rv)
        elif args[0] == 'patch':
            obj = self.objects[args[1], args[2]]
            for action in stdin:
                tokens = [x.replace('~1', '/').replace('~0', '~') for x in action['path'][1:].split('/')]
                parent = obj
                for token in tokens[:-1]:
                    parent = parent[token]
                token = tokens[-1]
                if action['op'] == 'test':
                    if parent.get(token) != action['value']:
                        raise RuntimeError('precondition')
                elif action['op'] == 'remove':
                    del parent[token]
                else:
                    parent[token] = copy.deepcopy(action['value'])
            self.revision += 1
            obj['metadata']['resourceVersion'] = str(self.revision)
        elif args[0] == 'delete':
            path = args[1].removeprefix('--raw=')
            plural, name = path.split('/')[-2:]
            kind = next(k for k, v in RESOURCES.items() if v[2] == plural)
            obj = self.objects[kind, name]
            if stdin['preconditions'] != {key: obj['metadata'][key] for key in ('uid', 'resourceVersion')}:
                raise RuntimeError('precondition')
            del self.objects[kind, name]
        else:
            raise AssertionError('unexpected command')
        if self.hook:
            self.hook('after', args, stdin)
        return ''


class LedgerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.manifest = {'run_id': 'unit', 'namespace': 'astronomer-qualification-unit', 'context': 'test-context', 'scenarios': []}
        self.runner = runner.Runner(self.manifest, Path(self.temp.name) / 'report.json')
        self.api = self.runner.kubectl = FakeKubectl(self.manifest['namespace'])
        # Unique per test, preserving identity across recovery within each test.
        self.api.objects['namespace', 'kube-system']['metadata']['uid'] = self.temp.name

    def new_runner(self, path=None):
        result = runner.Runner(self.manifest, path or self.runner.evidence_path)
        result.kubectl = self.api
        return result

    def test_reverse_restore_and_server_preconditions(self):
        with Ledger(self.runner) as ledger:
            ledger.lock()
            ledger.change('deployment', 'agent', 'replicas', 0)
            ledger.change('deployment', 'agent', 'restart', '2026-10-06T00:00:00Z')
            self.assertTrue(ledger.cleanup())
        obj = self.api.get('deployment', 'agent')
        self.assertEqual(2, obj['spec']['replicas'])
        self.assertNotIn(RESTART, obj['spec']['template']['metadata']['annotations'])
        for args, body in self.api.calls:
            if args[0] == 'patch':
                self.assertEqual(['/metadata/uid', '/metadata/resourceVersion'], [p['path'] for p in body[:2]])
            if args[0] == 'delete':
                self.assertEqual(['delete', '--raw=/api/v1/namespaces/astronomer-qualification-unit/configmaps/qualification-drill-lock', '-f', '-'], args)
                self.assertEqual({'uid', 'resourceVersion'}, set(body['preconditions']))

    def test_intent_durable_before_mutation_and_crash_resume(self):
        def crash(phase, args, body):
            if phase == 'after' and args[0] == 'patch':
                disk = json.loads(Path(str(self.runner.evidence_path) + '.ledger.json').read_text())
                self.assertEqual('intent', disk['operations'][-1]['state'])
                raise Crash()
        with self.assertRaises(Crash):
            with Ledger(self.runner) as ledger:
                ledger.lock()
                self.api.hook = crash
                ledger.change('deployment', 'agent', 'replicas', 0)
        self.api.hook = None
        with Ledger(self.new_runner(), resume=True) as ledger:
            ledger.lock()
            self.assertTrue(ledger.cleanup())
        self.assertEqual(2, self.api.get('deployment', 'agent')['spec']['replicas'])
        with Ledger(self.new_runner(), resume=True) as ledger:
            ledger.lock()
            self.assertTrue(ledger.cleanup())

    def test_ambiguous_create_exact_nonce_reconciles_without_template_persistence(self):
        def timeout(phase, args, body):
            if phase == 'after' and args[0] == 'create' and body['kind'] == 'Job':
                raise RuntimeError('timeout')
        with Ledger(self.runner) as ledger:
            ledger.lock()
            self.api.hook = timeout
            op = ledger.create('job', 'one', {'apiVersion': 'batch/v1', 'kind': 'Job', 'spec': {'sensitive_template_marker': True}})
            self.assertEqual('mutated', op['state'])
            self.assertNotIn('sensitive_template_marker', ledger.path.read_text())
            self.api.hook = None
            self.assertTrue(ledger.cleanup())

    def test_ambiguous_absent_create_retains_lock(self):
        with Ledger(self.runner) as ledger:
            ledger.lock()
            def timeout(phase, args, body):
                if phase == 'before' and args[0] == 'create':
                    raise RuntimeError('timeout')
            self.api.hook = timeout
            with self.assertRaises(LedgerError):
                ledger.create('job', 'one', {'apiVersion': 'batch/v1', 'kind': 'Job'})
            self.api.hook = None
            self.assertFalse(ledger.cleanup())
            self.assertIsNotNone(self.api.get_optional('configmap', 'qualification-drill-lock'))

    def test_mutation_between_reads_is_never_overwritten(self):
        with Ledger(self.runner) as ledger:
            ledger.lock()
            reads = 0
            def intervene(kind, name):
                nonlocal reads
                if kind == 'deployment':
                    reads += 1
                    if reads == 2:
                        self.api.objects[kind, name]['spec']['replicas'] = 3
                        self.api.objects[kind, name]['metadata']['resourceVersion'] = 'external'
            self.api.get_hook = intervene
            with self.assertRaises(LedgerError):
                ledger.change('deployment', 'agent', 'replicas', 0)
            self.assertEqual(3, self.api.get('deployment', 'agent')['spec']['replicas'])
            self.assertFalse(ledger.cleanup())
            self.assertFalse(any(args[0] == 'patch' for args, _ in self.api.calls))

    def test_restore_conflict_replacement_and_missing_labels(self):
        for alteration in ('field', 'uid', 'labels'):
            with self.subTest(alteration=alteration):
                self.setUp()
                with Ledger(self.runner) as ledger:
                    ledger.lock()
                    ledger.change('deployment', 'agent', 'replicas', 0)
                    obj = self.api.objects['deployment', 'agent']
                    if alteration == 'field': obj['spec']['replicas'] = 3
                    if alteration == 'uid': obj['metadata']['uid'] = 'replacement'
                    if alteration == 'labels': obj['metadata']['labels'] = {}
                    before = len(self.api.calls)
                    self.assertFalse(ledger.cleanup())
                    self.assertEqual(before, len(self.api.calls))
                    self.assertFalse(ledger.data['cleanup_complete'])

    def test_cluster_namespace_manifest_host_and_location_mismatch(self):
        with Ledger(self.runner) as ledger:
            ledger.lock()
        for mismatch in ('cluster', 'namespace', 'manifest', 'host', 'location'):
            with self.subTest(mismatch=mismatch):
                api_before = copy.deepcopy(self.api.objects)
                other = self.new_runner()
                if mismatch in ('cluster', 'namespace'):
                    name = 'kube-system' if mismatch == 'cluster' else self.manifest['namespace']
                    self.api.objects['namespace', name]['metadata']['uid'] += '-changed'
                if mismatch == 'manifest': other.report['manifest_digest'] = 'changed'
                if mismatch == 'location':
                    other.evidence_path = Path(self.temp.name) / 'copied.json'
                    target = Path(str(other.evidence_path) + '.ledger.json')
                    target.write_bytes(Path(str(self.runner.evidence_path) + '.ledger.json').read_bytes())
                    target.chmod(0o600)
                original_identity = Ledger.identity
                def identity(instance):
                    value = original_identity(instance)
                    if mismatch == 'host': value['recovery_host'] = 'other-host'
                    return value
                with patch.object(Ledger, 'identity', identity):
                    with self.assertRaises(LedgerError):
                        with Ledger(other, resume=True): pass
                self.api.objects = api_before

    def test_namespace_lock_covers_different_ledger_paths_and_existing_cluster_lock(self):
        other = self.new_runner(Path(self.temp.name) / 'other.json')
        with Ledger(self.runner) as ledger:
            ledger.lock()
            with self.assertRaises(BlockingIOError):
                with Ledger(other): pass
        with Ledger(other) as ledger:
            with self.assertRaises(LedgerError): ledger.lock()
            self.assertEqual([], ledger.data['operations'])

    def test_preexisting_resource_never_adopted(self):
        with Ledger(self.runner) as ledger:
            ledger.lock()
            self.api.put('networkpolicy', 'existing')
            with self.assertRaises(LedgerError):
                ledger.create('networkpolicy', 'existing', {})
            self.assertEqual(1, len(ledger.data['operations']))
            self.assertTrue(ledger.cleanup())
            self.assertIsNotNone(self.api.get_optional('networkpolicy', 'existing'))

    def test_cleanup_delete_crash_before_receipt_then_resume(self):
        with self.assertRaises(Crash):
            with Ledger(self.runner) as ledger:
                ledger.lock()
                def crash(phase, args, body):
                    if phase == 'after' and args[0] == 'delete': raise Crash()
                self.api.hook = crash
                ledger.cleanup()
        self.api.hook = None
        with Ledger(self.new_runner(), resume=True) as ledger:
            ledger.lock()
            self.assertTrue(ledger.cleanup())
        self.api.put('configmap', 'qualification-drill-lock')
        with self.assertRaises(LedgerError):
            with Ledger(self.new_runner(), resume=True) as ledger: ledger.lock()

    def test_delete_pod_uid_guard_and_irreversible_record(self):
        with Ledger(self.runner) as ledger:
            ledger.lock()
            pod = self.api.put('pod', 'selected')
            op = ledger.delete_pod(copy.deepcopy(pod))
            self.assertTrue(op['irreversible'])
            self.assertIsNone(self.api.get_optional('pod', 'selected'))
            self.api.put('pod', 'selected')
            self.assertFalse(ledger.cleanup())
            self.assertIsNotNone(self.api.get_optional('pod', 'selected'))

    def test_server_precondition_rejects_race_after_read(self):
        with Ledger(self.runner) as ledger:
            ledger.lock()
            def race(phase, args, body):
                if phase == 'before' and args[0] == 'delete':
                    self.api.objects['configmap', 'qualification-drill-lock']['metadata']['resourceVersion'] = 'new'
            self.api.hook = race
            self.assertFalse(ledger.cleanup())
            self.assertIsNotNone(self.api.get_optional('configmap', 'qualification-drill-lock'))

    def test_ambiguous_unapplied_field_fails_closed(self):
        with Ledger(self.runner) as ledger:
            ledger.lock()
            def timeout(phase, args, body):
                if phase == 'before' and args[0] == 'patch': raise RuntimeError('timeout')
            self.api.hook = timeout
            with self.assertRaises(RuntimeError): ledger.change('deployment', 'agent', 'replicas', 0)
            self.api.hook = None
            self.assertFalse(ledger.cleanup())

    def test_resume_preserves_failed_history_and_separates_cleanup(self):
        with Ledger(self.runner) as ledger:
            ledger.lock()
            ledger.change('deployment', 'agent', 'replicas', 0)
        self.runner.report.update(status='failed', scenarios=[{'id': 'one', 'status': 'failed'}], errors=['RuntimeError'])
        self.runner.checkpoint()
        report = self.new_runner().run(resume=True)
        self.assertEqual('failed', report['status'])
        self.assertEqual('passed', report['cleanup_status'])
        self.assertEqual([{'id': 'one', 'status': 'failed'}], report['scenarios'])
        self.assertEqual(['RuntimeError'], report['errors'])

    def test_resume_without_report_marks_interrupted(self):
        with Ledger(self.runner) as ledger:
            ledger.lock()
        report = self.new_runner().run(resume=True)
        self.assertEqual('interrupted', report['status'])
        self.assertEqual('passed', report['cleanup_status'])
        self.assertEqual([], report['scenarios'])

    def test_crash_after_lock_create_recovers_exact_nonce(self):
        def crash(phase, args, body):
            if phase == 'after' and args[0] == 'create': raise Crash()
        self.api.hook = crash
        with self.assertRaises(Crash):
            with Ledger(self.runner) as ledger: ledger.lock()
        self.api.hook = None
        with Ledger(self.new_runner(), resume=True) as ledger:
            ledger.lock()
            self.assertTrue(ledger.cleanup())

    def test_wrong_creation_nonce_never_adopted_on_resume(self):
        with Ledger(self.runner) as ledger: ledger.lock()
        self.api.objects['configmap', 'qualification-drill-lock']['metadata']['labels'][NONCE] = 'other'
        before = len(self.api.calls)
        with self.assertRaises(LedgerError):
            with Ledger(self.new_runner(), resume=True) as ledger: ledger.lock()
        self.assertEqual(before, len(self.api.calls))

    def test_cluster_identity_permission_failure_prevents_mutation(self):
        del self.api.objects['namespace', 'kube-system']
        with self.assertRaises(RuntimeError):
            with Ledger(self.runner): pass
        self.assertEqual([], self.api.calls)

    def test_symlink_and_public_local_lock_directory_rejected(self):
        for unsafe in ('symlink', 'public'):
            with self.subTest(unsafe=unsafe), tempfile.TemporaryDirectory() as base:
                directory = Path(base) / ('astronomer-drill-locks-' + str(os.getuid()))
                if unsafe == 'symlink': directory.symlink_to(self.temp.name)
                else: directory.mkdir(mode=0o755)
                with patch('resilience_ledger.LOCK_ROOT', base):
                    with self.assertRaises(LedgerError):
                        with Ledger(self.runner): pass
                self.assertEqual([], self.api.calls)

    def test_non_timestamp_annotation_refused_before_persistence(self):
        self.api.objects['deployment', 'agent']['spec']['template']['metadata']['annotations'] = {RESTART: 'not-a-timestamp'}
        with Ledger(self.runner) as ledger:
            ledger.lock()
            with self.assertRaises(LedgerError):
                ledger.change('deployment', 'agent', 'restart', '2026-10-06T00:00:00Z')
            self.assertNotIn('not-a-timestamp', ledger.path.read_text())
            self.assertEqual(1, len(ledger.data['operations']))
            self.assertTrue(ledger.cleanup())

    def test_kubectl_errors_sanitized_and_output_bounded(self):
        executable = Path(self.temp.name) / 'kubectl'
        for code in ("import sys; sys.stderr.write('synthetic-private-output'); sys.exit(1)",
                     "import sys; sys.stdout.write('x' * (5 << 20))"):
            executable.write_text('#!' + sys.executable + '\n' + code + '\n')
            executable.chmod(0o700)
            with patch.dict(os.environ, {'PATH': self.temp.name}):
                with self.assertRaises(runner.DrillError) as caught:
                    runner.Kubectl('test-context', self.manifest['namespace']).run(['get', 'pods'])
                self.assertNotIn('synthetic-private-output', str(caught.exception))

    def test_resume_lock_independent_of_temporary_environment(self):
        with Ledger(self.runner) as ledger:
            ledger.lock()
        with Ledger(self.new_runner(), resume=True) as ledger:
            ledger.lock()
            with patch.dict(os.environ, {'TMPDIR': self.temp.name, 'TEMP': self.temp.name, 'TMP': self.temp.name}):
                with self.assertRaises(BlockingIOError):
                    with Ledger(self.new_runner(), resume=True): pass
            self.assertTrue(ledger.cleanup())

    def test_restore_crash_after_patch_is_idempotent(self):
        with self.assertRaises(Crash):
            with Ledger(self.runner) as ledger:
                ledger.lock()
                ledger.change('deployment', 'agent', 'replicas', 0)
                def crash(phase, args, body):
                    if phase == 'after' and args[0] == 'patch': raise Crash()
                self.api.hook = crash
                ledger.cleanup()
        self.api.hook = None
        with Ledger(self.new_runner(), resume=True) as ledger:
            ledger.lock()
            self.assertTrue(ledger.cleanup())
        self.assertEqual(2, self.api.get('deployment', 'agent')['spec']['replicas'])

    def test_runner_resume_does_not_replay_scenarios(self):
        self.runner.manifest['scenarios'] = [{'id': 'one', 'steps': [{'action': 'scale_workload', 'kind': 'deployment', 'name': 'agent', 'replicas': 0}]}]
        self.runner.report['manifest_digest'] = runner.canonical_digest(self.manifest)
        report = self.runner.run()
        self.assertEqual('passed', report['status'])
        before = len(self.api.calls)
        other = self.new_runner()
        self.assertEqual('passed', other.run(resume=True)['status'])
        self.assertEqual(before, len(self.api.calls))
        self.assertFalse(other.report['release_eligible'])


if __name__ == '__main__':
    unittest.main()
