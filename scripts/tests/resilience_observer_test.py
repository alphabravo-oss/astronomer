"""Hermetic SSE, receipt and cleanup tests: no listener sockets or cluster calls."""
import copy
import io
import socket
import json
import os
from pathlib import Path
import queue
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from resilience_ledger_test import FakeKubectl, runner, Ledger, LedgerError, Crash
import resilience_observer as observer
from resilience_ledger import OBSERVATION

CLUSTER = '00000000-0000-4000-8000-000000000001'
CONFIG = {'base_url': 'https://example.invalid', 'cluster_id': CLUSTER, 'bearer_token_file': '/unused'}


def event(scope, rv):
    return ('data: ' + json.dumps({'type': 'cluster.k8s_changed', 'data': {**scope, 'resource_version': rv}}) + '\n\n').encode()


class Stream:
    def __init__(self):
        self.queue = queue.Queue()
        self.closed = False
        self.read_seen = threading.Event()

    def read(self):
        item = self.queue.get(timeout=2)
        self.read_seen.set()
        if isinstance(item, Exception): raise item
        return item

    def close(self):
        self.closed = True
        self.queue.put(b'')


class Transport:
    def __init__(self, api, namespace):
        self.api, self.namespace = api, namespace
        self.stream = None
        self.gets = []
        self.mapping_wrong = False

    def open(self, path, deadline, sse=False):
        assert path == '/api/v1/events/stream/' and sse
        self.stream = Stream()
        self.stream.queue.put(b': connected\n\n')
        return self.stream

    def get(self, path, deadline):
        self.gets.append(path)
        name = path.rsplit('/', 1)[-1]
        value = self.api.get('namespace', name)
        if self.mapping_wrong: value['metadata']['uid'] = 'wrong-cluster'
        return value


class ReceiptAPI(FakeKubectl):
    def run(self, args, stdin=None, timeout=60):
        # Preserve a server response snapshot before the simulated post-response race.
        if args[0] != 'patch' or args[-2:] != ['-o', 'json']:
            return super().run(args, stdin, timeout)
        hook = self.hook
        self.hook = None
        try:
            if hook: hook('before', args, stdin)
            super().run(args, stdin, timeout)
            response = json.dumps(self.objects[args[1], args[2]])
            if hook: hook('after', args, stdin)
            return response
        finally:
            self.hook = hook


class FastClock:
    def __init__(self):
        self.now = 1000.0

    def monotonic(self): return self.now
    def monotonic_ns(self): return int(self.now * 1e9)
    def sleep(self, seconds): self.now += seconds


class ObserverTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.manifest = {'run_id': 'unit', 'namespace': 'astronomer-qualification-unit', 'context': 'test-context', 'scenarios': [], 'observer': CONFIG}
        self.runner = runner.Runner(self.manifest, Path(self.temp.name) / 'report.json')
        self.api = self.runner.kubectl = ReceiptAPI(self.manifest['namespace'])
        self.api.objects['namespace', 'kube-system']['metadata']['uid'] = self.temp.name
        obj = self.api.objects['deployment', 'agent']
        obj['metadata']['generation'] = 1
        obj['status'] = {'observedGeneration': 1, 'replicas': 2, 'updatedReplicas': 2, 'availableReplicas': 2, 'readyReplicas': 2}
        self.transport = Transport(self.api, self.manifest['namespace'])
        self.scope = {'cluster_id': CLUSTER, 'kind': 'Deployment', 'api_group': 'apps', 'api_version': 'v1', 'namespace': self.manifest['namespace'], 'name': 'agent'}
        self.step = {'action': 'observe_annotation', 'kind': 'deployment', 'name': 'agent', 'repetitions': 1, 'timeout_seconds': 30}

    def test_url_policy_and_strict_config(self):
        for base in ('https://example.invalid', 'http://127.0.0.1:8001', 'http://[::1]:8001'):
            observer.validate_config({**CONFIG, 'base_url': base})
        for base in ('http://localhost:8001', 'http://example.invalid', 'https://user@example.invalid', 'https://example.invalid/x', 'https://example.invalid?token=x', 'https://example.invalid#fragment', 'https://example.invalid:0'):
            with self.subTest(base=base), self.assertRaises(observer.ObservationError):
                observer.validate_config({**CONFIG, 'base_url': base})
        with self.assertRaises(observer.ObservationError): observer.validate_config({**CONFIG, 'token': 'forbidden'})

    def test_private_bearer_file_no_symlink_or_header_injection(self):
        path = Path(self.temp.name) / 'bearer'
        path.write_text('synthetic-token')
        path.chmod(0o600)
        self.assertEqual('synthetic-token', observer.read_bearer(path))
        alias = path.with_suffix('.alias'); alias.symlink_to(path)
        with self.assertRaises(observer.ObservationError): observer.read_bearer(alias)
        for content in ('one\r\nHeader: injected', 'x' * 8193):
            path.write_text(content)
            with self.assertRaises(observer.ObservationError): observer.read_bearer(path)
        path.write_text('synthetic-token'); path.chmod(0o644)
        with self.assertRaises(observer.ObservationError): observer.read_bearer(path)

    def test_fragmented_multiline_crlf_and_frame_limit(self):
        frames = observer.Frames()
        self.assertEqual([], frames.feed(b': connec'))
        self.assertEqual([], frames.feed(b'ted\r\n'))
        self.assertEqual([[': connected']], frames.feed(b'\r\n'))
        lines = frames.feed(b'data: {"type":\r\ndata: "sys.ping"}\r\n\r\n')[0]
        listener = observer.Listener.__new__(observer.Listener)
        listener.scope = self.scope; listener.connected = False; listener.candidates = {}
        listener.frame(lines, 1)
        self.assertEqual({}, listener.candidates)
        with self.assertRaises(observer.ObservationError): observer.Frames().feed(b'x' * 65537)
        frames.total_bytes = 8 << 20
        with self.assertRaises(observer.ObservationError): frames.feed(b'x')

    def test_scope_and_opaque_resource_version_exact_matching(self):
        listener = observer.Listener.__new__(observer.Listener)
        listener.scope = self.scope; listener.connected = False; listener.candidates = {}
        for key in self.scope:
            wrong = {**self.scope, key: 'different'}
            listener.frame(observer.Frames().feed(event(wrong, 'opaque:1'))[0], 10)
        self.assertEqual({}, listener.candidates)
        listener.frame(observer.Frames().feed(event(self.scope, 'opaque:1'))[0], 11)
        self.assertEqual({'opaque:1': 11}, listener.candidates)
        listener.condition = threading.Condition(); listener.failure = None
        self.assertEqual(11, listener.wait(time.monotonic() + 1, 'opaque:1'))
        with self.assertRaises(observer.ObservationError): listener.wait(time.monotonic() - 1, 'opaque:2')
        with self.assertRaises(observer.ObservationError): listener.wait(time.monotonic() - 1, 'opaque:1', 12)

    def test_candidate_limit_malformed_frame_and_disconnect(self):
        listener = observer.Listener.__new__(observer.Listener)
        listener.scope = self.scope; listener.connected = False
        listener.candidates = {str(i): i for i in range(256)}
        with self.assertRaises(observer.ObservationError): listener.frame(observer.Frames().feed(event(self.scope, 'overflow'))[0], 1)
        transport = self.transport
        for payload in (b'data: not-json\n\n', b''):
            listener = observer.Listener(transport, self.scope, time.monotonic() + 1)
            listener.wait(time.monotonic() + 1)
            transport.stream.queue.put(payload)
            with self.assertRaises(observer.ObservationError): listener.wait(time.monotonic() + 1, 'unseen')
            listener.close()

    def test_connected_requires_complete_frame(self):
        original_open = self.transport.open
        def partial(*args, **kwargs):
            stream = original_open(*args, **kwargs)
            stream.queue.get_nowait()
            stream.queue.put(b': connected\n')
            return stream
        self.transport.open = partial
        listener = observer.Listener(self.transport, self.scope, time.monotonic() + 2)
        with self.assertRaises(observer.ObservationError): listener.wait(time.monotonic() + .02)
        self.transport.stream.queue.put(b'\n')
        listener.wait(time.monotonic() + 1)
        listener.close()

    def test_mapping_mismatch_prevents_annotation_mutation(self):
        with Ledger(self.runner) as ledger:
            self.runner.ledger = ledger; ledger.lock()
            self.transport.mapping_wrong = True
            with self.assertRaises(observer.ObservationError):
                observer.observe(self.runner, self.step, lambda _: self.transport)
            self.assertEqual(1, len(ledger.data['operations']))
            self.assertEqual('cluster_mapping_mismatch', self.runner.report['observations'][0]['trials'][0]['code'])
            self.assertTrue(ledger.cleanup())

    def test_event_before_patch_ack_is_measured_and_restored(self):
        with Ledger(self.runner) as ledger:
            self.runner.ledger = ledger; ledger.lock()
            listeners = []
            def make_listener(*args):
                value = observer.Listener(*args); listeners.append(value); return value
            def publish(phase, args, body):
                if phase == 'after' and args[0] == 'patch' and self.api.objects['deployment', 'agent']['metadata'].get('annotations', {}).get(OBSERVATION):
                    self.assertTrue(listeners[-1].connected)
                    rv = self.api.objects['deployment', 'agent']['metadata']['resourceVersion']
                    self.transport.stream.queue.put(event(self.scope, rv))
                    # Force event consumption while the patch response is withheld.
                    arrival = listeners[-1].wait(time.monotonic() + 1, rv)
                    self.assertLessEqual(arrival, time.monotonic_ns())
            self.api.hook = publish
            with patch.object(observer.time, 'sleep'):
                observer.observe(self.runner, self.step, lambda _: self.transport, make_listener)
            trial = self.runner.report['observations'][0]['trials'][0]
            self.assertEqual('succeeded', trial['status'])
            self.assertEqual('passed', trial['cleanup_status'])
            self.assertNotIn(OBSERVATION, self.api.get('deployment', 'agent')['metadata']['annotations'])
            op = ledger.data['operations'][1]
            self.assertIn('resource_version', op['observation'])
            public = json.dumps(self.runner.report)
            self.assertNotIn(op['expected'], public)
            self.assertNotIn('resource_version', public)
            self.assertTrue(ledger.cleanup())

    def scripted_trial(self, behavior, repetitions=1):
        clock = FastClock()
        class ScriptedListener:
            def __init__(inner, *args): pass
            def wait(inner, deadline, rv=None, start_ns=0):
                if rv is None: return
                if behavior in ('miss', 'wrong_rv'):
                    clock.now = deadline + .001
                    raise observer.ObservationError('deadline_exceeded')
                clock.now += .01
                return clock.monotonic_ns()
            def close(inner): pass
        step = {**self.step, 'repetitions': repetitions}
        with Ledger(self.runner) as ledger:
            self.runner.ledger = ledger; ledger.lock()
            def intervention(phase, args, body):
                if phase != 'after' or args[0] != 'patch': return
                obj = self.api.objects['deployment', 'agent']
                if OBSERVATION not in obj['metadata'].get('annotations', {}): return
                if behavior == 'replaced': obj['metadata']['uid'] = 'replacement'
                if behavior == 'lost_receipt': raise RuntimeError('synthetic-remote-private')
                if behavior == 'external_change': obj['metadata']['annotations'][OBSERVATION] = 'q-' + 'f' * 32
            self.api.hook = intervention
            with patch.object(observer.time, 'monotonic', clock.monotonic), patch.object(observer.time, 'monotonic_ns', clock.monotonic_ns), patch.object(observer.time, 'sleep', clock.sleep):
                if behavior == 'success':
                    observer.observe(self.runner, step, lambda _: self.transport, ScriptedListener)
                else:
                    with self.assertRaises(observer.ObservationError):
                        observer.observe(self.runner, step, lambda _: self.transport, ScriptedListener)
            return copy.deepcopy(self.runner.report['observations'][0]), copy.deepcopy(ledger.data)

    def test_missed_event_and_deadline_keep_unattempted_trials(self):
        report, ledger = self.scripted_trial('miss', repetitions=3)
        self.assertEqual(1, report['missed'])
        self.assertEqual(2, report['not_run'])
        self.assertIsNone(report['p95_ms'])
        self.assertEqual('done', ledger['operations'][1]['state'])

    def test_replacement_and_cleanup_conflict_never_pass(self):
        for behavior in ('replaced', 'external_change'):
            with self.subTest(behavior=behavior):
                self.setUp()
                report, ledger = self.scripted_trial(behavior)
                self.assertEqual(0, report['succeeded'])
                self.assertEqual('unresolved', report['trials'][0]['cleanup_status'])
                self.assertEqual('unresolved', ledger['operations'][1]['state'])

    def test_lost_patch_receipt_unobserved_but_cleanup_restores(self):
        report, ledger = self.scripted_trial('lost_receipt')
        self.assertEqual(0, report['succeeded'])
        self.assertEqual('passed', report['trials'][0]['cleanup_status'])
        self.assertEqual('done', ledger['operations'][1]['state'])
        self.assertNotIn('synthetic-remote-private', json.dumps(report))

    def test_p95_requires_twenty_and_all_complete(self):
        result = {'trials': [{'status': 'succeeded', 'latency_ms': i} for i in range(20)]}
        observer.summarize(result)
        self.assertEqual(18, result['p95_ms'])
        result['trials'][0]['status'] = 'missed'
        observer.summarize(result)
        self.assertIsNone(result['p95_ms'])
        for trial in result['trials']: trial['status'] = 'missed'
        observer.summarize(result)
        self.assertEqual(20, result['missed']); self.assertIsNone(result['p95_ms'])
        result['trials'] = [{'status': 'succeeded', 'latency_ms': 1}]
        observer.summarize(result); self.assertIsNone(result['p95_ms'])

    def test_manifest_observer_optional_and_operation_bound(self):
        manifest = {**self.manifest, 'schema_version': runner.SCHEMA,
                    'scenarios': [{'id': 'observe', 'steps': [self.step]}]}
        runner.validate_manifest(manifest)
        missing = copy.deepcopy(manifest); del missing['observer']
        with self.assertRaises(runner.DrillError): runner.validate_manifest(missing)
        invalid = copy.deepcopy(manifest); invalid['scenarios'][0]['steps'][0]['timeout_seconds'] = 1801
        with self.assertRaises(runner.DrillError): runner.validate_manifest(invalid)
        invalid = copy.deepcopy(manifest); invalid['scenarios'][0]['steps'] = [{**self.step, 'repetitions': 100}] * 100
        invalid['scenarios'].append({'id': 'extra', 'steps': [self.step]})
        with self.assertRaises(runner.DrillError): runner.validate_manifest(invalid)

    def test_connection_close_socket_is_interrupted_without_reader_leak(self):
        left, right = socket.socketpair()
        self.addCleanup(right.close)
        conn = observer.http.client.HTTPConnection('unused.invalid')
        conn.sock = left
        conn._HTTPConnection__state = observer.http.client._CS_REQ_SENT
        stream = observer.HTTPStream(conn, time.monotonic() + .15)
        stream.socket = left
        right.sendall(b'HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\n: connected\n\n')
        stream.response = conn.getresponse()
        self.assertIsNone(conn.sock)
        with patch.object(self.transport, 'open', return_value=stream):
            listener = observer.Listener(self.transport, self.scope, time.monotonic() + 1)
            listener.wait(time.monotonic() + 1)
            with self.assertRaises(observer.ObservationError): listener.wait(time.monotonic() + 1, 'missing')
            listener.close()
            self.assertFalse(listener.thread.is_alive())

    def test_late_dns_result_cannot_create_socket_or_send_credentials(self):
        released, finished = threading.Event(), threading.Event()
        def resolve(*args, **kwargs):
            released.wait(1)
            finished.set()
            return [(socket.AF_INET, socket.SOCK_STREAM, 0, '', ('127.0.0.1', 1))]
        class Connection:
            sock = None
            def close(inner): pass
        stream = observer.HTTPStream(Connection(), time.monotonic() + 1)
        try:
            with patch.object(observer.socket, 'getaddrinfo', resolve), patch.object(observer.socket, 'socket') as new_socket:
                with self.assertRaises(observer.ObservationError): stream.connect(('unused.invalid', 443), time.monotonic() + .01)
                stream.abort()
                released.set()
                self.assertTrue(finished.wait(1))
                new_socket.assert_not_called()
        finally:
            released.set(); stream.close()

    def test_late_connected_socket_closed_before_request_continues(self):
        clock = FastClock()
        class Sock:
            closed = False
            def settimeout(inner, value): pass
            def connect(inner, target): clock.now += 10
            def close(inner): inner.closed = True
            def shutdown(inner, value): pass
        class Connection:
            sock = None
            def close(inner): pass
        sock = Sock()
        stream = observer.HTTPStream(Connection(), time.monotonic() + 1)
        try:
            with patch.object(observer.time, 'monotonic', clock.monotonic), patch.object(observer.socket, 'getaddrinfo', return_value=[(socket.AF_INET, socket.SOCK_STREAM, 0, '', ('127.0.0.1', 1))]), patch.object(observer.socket, 'socket', return_value=sock):
                with self.assertRaises(observer.ObservationError): stream.connect(('unused.invalid', 443), clock.now + 1)
            self.assertTrue(sock.closed)
        finally: stream.close()

    def test_kubectl_uses_remaining_deadline_not_nominal_timeout(self):
        class Process:
            def __init__(inner):
                inner.stdout = io.BytesIO(b'{}'); inner.stdin = io.BytesIO(); inner.returncode = 0; inner.timeout = None
            def wait(inner, timeout=None): inner.timeout = timeout; return 0
        process = Process()
        with patch.object(runner.subprocess, 'Popen', return_value=process), patch.object(runner.time, 'monotonic', return_value=100):
            api = runner.Kubectl('context', 'namespace', deadline=105)
            api.run(['get', 'deployment', 'one'], timeout=60)
            self.assertEqual(5, process.timeout)
            api.deadline = 99
            with self.assertRaises(runner.DrillError): api.run(['get', 'deployment', 'one'])

    def test_quiet_period_obeys_whole_step_deadline(self):
        clock = FastClock()
        with Ledger(self.runner) as ledger:
            self.runner.ledger = ledger; ledger.lock()
            with patch.object(observer.time, 'monotonic', clock.monotonic), patch.object(observer.time, 'sleep', clock.sleep):
                with self.assertRaises(observer.ObservationError):
                    observer.observe(self.runner, {**self.step, 'repetitions': 3, 'timeout_seconds': 1}, lambda _: self.transport)
            result = self.runner.report['observations'][0]
            self.assertEqual(1, result['timeout']); self.assertEqual(2, result['not_run'])
            self.assertEqual(1, len(ledger.data['operations']))
            self.assertTrue(ledger.cleanup())

    def test_arbitrary_exception_text_not_reported(self):
        def factory(_): raise observer.ObservationError('synthetic-private-payload')
        with Ledger(self.runner) as ledger:
            self.runner.ledger = ledger; ledger.lock()
            with self.assertRaises(observer.ObservationError): observer.observe(self.runner, self.step, factory)
            self.assertNotIn('synthetic-private-payload', json.dumps(self.runner.report))
            self.assertEqual('observer_failed', self.runner.report['observations'][0]['setup_code'])
            self.assertTrue(ledger.cleanup())

    def test_observation_intent_crash_resumes_annotation_restore(self):
        with self.assertRaises(Crash):
            with Ledger(self.runner) as ledger:
                self.runner.ledger = ledger; ledger.lock()
                def crash(phase, args, body):
                    if phase == 'after' and args[0] == 'patch': raise Crash()
                self.api.hook = crash
                ledger.observe_annotation('agent', self.api.get('deployment', 'agent'), time.monotonic() + 1)
        self.api.hook = None
        recovered = runner.Runner(self.manifest, self.runner.evidence_path)
        recovered.kubectl = self.api
        result = recovered.run(resume=True)
        self.assertEqual('interrupted', result['status'])
        self.assertEqual('passed', result['cleanup_status'])
        self.assertNotIn(OBSERVATION, self.api.get('deployment', 'agent')['metadata']['annotations'])

    def test_trial_requests_use_shorter_deadline_cleanup_has_separate_drain(self):
        clock = FastClock()
        observed = []
        class ReadyListener:
            def __init__(inner, *args): pass
            def wait(inner, deadline, rv=None, start_ns=0):
                if rv is not None:
                    clock.now += .01
                    return clock.monotonic_ns()
            def close(inner): pass
        with Ledger(self.runner) as ledger:
            self.runner.ledger = ledger; ledger.lock()
            def record(phase, args, body):
                if phase == 'before' and args[0] == 'patch': observed.append(self.api.deadline)
            self.api.hook = record
            with patch.object(observer.time, 'monotonic', clock.monotonic), patch.object(observer.time, 'monotonic_ns', clock.monotonic_ns), patch.object(observer.time, 'sleep', clock.sleep):
                observer.observe(self.runner, {**self.step, 'timeout_seconds': 1800}, lambda _: self.transport, ReadyListener)
            self.assertEqual([1122.0, None], observed)
            self.assertTrue(ledger.cleanup())

    def test_cleanup_time_invalidates_step_success_when_budget_exhausted(self):
        clock = FastClock()
        class ReadyListener:
            def __init__(inner, *args): pass
            def wait(inner, deadline, rv=None, start_ns=0):
                if rv is not None: return clock.monotonic_ns()
            def close(inner): clock.now += 40
        with Ledger(self.runner) as ledger:
            self.runner.ledger = ledger; ledger.lock()
            with patch.object(observer.time, 'monotonic', clock.monotonic), patch.object(observer.time, 'monotonic_ns', clock.monotonic_ns), patch.object(observer.time, 'sleep', clock.sleep):
                with self.assertRaises(observer.ObservationError):
                    observer.observe(self.runner, self.step, lambda _: self.transport, ReadyListener)
            trial = self.runner.report['observations'][0]['trials'][0]
            self.assertEqual('step_deadline_exceeded', trial['code'])
            self.assertEqual('passed', trial['cleanup_status'])
            self.assertEqual('done', ledger.data['operations'][1]['state'])
            self.assertTrue(ledger.cleanup())

    def test_deadline_before_subscription_barrier_never_patches(self):
        clock = FastClock()
        class UnreadyListener:
            def __init__(inner, *args): pass
            def wait(inner, deadline, *args):
                clock.now = deadline + .01
                raise observer.ObservationError('deadline_exceeded')
            def close(inner): pass
        with Ledger(self.runner) as ledger:
            self.runner.ledger = ledger; ledger.lock()
            with patch.object(observer.time, 'monotonic', clock.monotonic), patch.object(observer.time, 'sleep', clock.sleep):
                with self.assertRaises(observer.ObservationError):
                    observer.observe(self.runner, self.step, lambda _: self.transport, UnreadyListener)
            self.assertFalse(any(args[0] == 'patch' for args, _ in self.api.calls))
            self.assertTrue(ledger.cleanup())

    def test_tls_retained_before_stalled_handshake_and_no_late_authorization(self):
        path = Path(self.temp.name) / 'bearer'; path.write_text('synthetic-token'); path.chmod(0o600)
        for late_completion in (False, True):
            with self.subTest(late_completion=late_completion):
                class Raw:
                    def setsockopt(inner, *args): pass
                    def shutdown(inner, *args): pass
                    def close(inner): pass
                raw = Raw()
                requests = []
                class TLS:
                    closed = False
                    shutdown_seen = threading.Event()
                    def settimeout(inner, seconds): pass
                    def do_handshake(inner):
                        self.assertTrue(inner.shutdown_seen.wait(1))
                        if not late_completion: raise OSError('synthetic-handshake-failure')
                    def shutdown(inner, how): inner.shutdown_seen.set()
                    def close(inner): inner.closed = True
                tls = TLS()
                class Context:
                    def wrap_socket(inner, sock, **kwargs):
                        self.assertIs(sock, raw)
                        self.assertEqual({'server_hostname': 'example.invalid', 'do_handshake_on_connect': False}, kwargs)
                        return tls
                client = observer.ObserverHTTP({**CONFIG, 'bearer_token_file': str(path)})
                client.tls = Context()
                def connect(stream, address, deadline):
                    stream.socket = raw
                    return raw
                try:
                    with patch.object(observer.HTTPStream, 'connect', connect), patch.object(observer.HTTPSConnection, 'request', side_effect=lambda *a, **k: requests.append(a)):
                        with self.assertRaises(observer.ObservationError):
                            client.open('/api/v1/events/stream/', time.monotonic() + .02, sse=True)
                    self.assertTrue(tls.shutdown_seen.is_set())
                    self.assertTrue(tls.closed)
                    self.assertEqual([], requests)
                finally:
                    raw.close()

    def test_source_fingerprint_preserves_module_boundaries(self):
        with patch.object(runner.Path, 'read_bytes', side_effect=[b'a', b'bc', b'']):
            first = runner.Runner(self.manifest, self.runner.evidence_path).report['source_digest']
        with patch.object(runner.Path, 'read_bytes', side_effect=[b'ab', b'c', b'']):
            second = runner.Runner(self.manifest, self.runner.evidence_path).report['source_digest']
        self.assertNotEqual(first, second)

    def test_transport_never_redirects_or_uses_env_proxy(self):
        path = Path(self.temp.name) / 'bearer'; path.write_text('synthetic-token'); path.chmod(0o600)
        class Response:
            status = 302
            def close(inner): pass
            def getheader(inner, name, default=''): return 'https://other.invalid'
        calls = []
        class Connection:
            sock = None
            def __init__(inner, host, port, **kwargs): calls.append(('connect', host))
            def connect(inner): pass
            def request(inner, method, route, headers): calls.append(('request', route))
            def getresponse(inner): return Response()
            def close(inner): pass
        with patch.dict(os.environ, {'HTTPS_PROXY': 'https://proxy.invalid', 'HTTP_PROXY': 'http://proxy.invalid'}), patch.object(observer, 'HTTPSConnection', Connection):
            client = observer.ObserverHTTP({**CONFIG, 'bearer_token_file': str(path)})
            with self.assertRaises(observer.ObservationError) as error: client.open('/api/v1/events/stream/', time.monotonic() + 1, sse=True)
            self.assertNotIn('synthetic-token', str(error.exception))
        self.assertEqual([('connect', 'example.invalid'), ('request', '/api/v1/events/stream/')], calls)


if __name__ == '__main__': unittest.main()
