"""Bounded, credential-file-only SSE observation for the existing drill runner."""
import http.client
import ipaddress
import json
import math
import os
import queue
import re
import socket
import ssl
import stat
import threading
import time
import urllib.parse
import uuid


class ObservationError(RuntimeError):
    """Only fixed local classifications may cross the report boundary."""


ERROR_CODES = frozenset({
    'cluster_mapping_mismatch',
    'deadline_exceeded',
    'deployment_not_quiet',
    'deployment_not_settled',
    'observation_state_unverified',
    'observation_step_incomplete',
    'observer_body_limit',
    'observer_config_invalid',
    'observer_connection_cancelled',
    'observer_connection_failed',
    'observer_content_type',
    'observer_credentials_invalid',
    'observer_http_status',
    'observer_resolution_timeout',
    'observer_response_invalid',
    'observer_tls_invalid',
    'observer_transport_failed',
    'stream_byte_limit',
    'stream_candidate_limit',
    'stream_disconnected',
    'stream_envelope_invalid',
    'stream_frame_count_limit',
    'stream_frame_limit',
    'stream_resource_version_invalid',
    'stream_shutdown_failed',
})


def failure_code(error):
    value = str(error) if isinstance(error, ObservationError) else None
    return value if value in ERROR_CODES else "observer_failed"


def validate_config(value):
    if not isinstance(value, dict) or set(value) - {'base_url', 'cluster_id', 'bearer_token_file', 'ca_file'} or not {'base_url', 'cluster_id', 'bearer_token_file'} <= set(value):
        raise ObservationError('observer_config_invalid')
    try:
        raw = value['base_url']
        if not isinstance(raw, str) or len(raw) > 2048 or any(c.isspace() for c in raw):
            raise ValueError()
        url = urllib.parse.urlsplit(raw)
        local = False
        try:
            local = ipaddress.ip_address(url.hostname).is_loopback
        except ValueError:
            pass
        if url.scheme != 'https' and not (url.scheme == 'http' and local):
            raise ValueError()
        if not url.hostname or url.username is not None or url.password is not None or url.query or url.fragment or url.path not in ('', '/'):
            raise ValueError()
        if url.port is not None and not 1 <= url.port <= 65535:
            raise ValueError()
        cluster_id = uuid.UUID(value['cluster_id'])
        if cluster_id.int == 0 or str(cluster_id) != value['cluster_id']:
            raise ValueError()
        for key in ('bearer_token_file', 'ca_file'):
            if key in value and (not isinstance(value[key], str) or not 1 <= len(value[key]) <= 4096 or '\x00' in value[key]):
                raise ValueError()
    except (ValueError, TypeError, AttributeError):
        raise ObservationError('observer_config_invalid') from None
    return value


def read_bearer(path):
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        try:
            info = os.fstat(fd)
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077 or info.st_size > 8192:
                raise ValueError()
            token = os.read(fd, 8193).decode('ascii').strip()
            if not re.fullmatch(r'[A-Za-z0-9._~+/=-]{1,8192}', token):
                raise ValueError()
            return token
        finally:
            os.close(fd)
    except (OSError, ValueError):
        raise ObservationError('observer_credentials_invalid') from None


def tls_context(ca_file=None):
    try:
        if ca_file is None:
            return ssl.create_default_context()
        fd = os.open(ca_file, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        try:
            info = os.fstat(fd)
            if not stat.S_ISREG(info.st_mode) or info.st_size > 1 << 20:
                raise ValueError()
            with os.fdopen(fd, 'rb', closefd=False) as handle:
                pem = handle.read((1 << 20) + 1)
            if len(pem) > 1 << 20:
                raise ValueError()
        finally:
            os.close(fd)
        return ssl.create_default_context(cadata=pem.decode('ascii'))
    except (OSError, ValueError):
        raise ObservationError('observer_tls_invalid') from None


def remaining(deadline):
    seconds = deadline - time.monotonic()
    if seconds <= 0:
        raise ObservationError('deadline_exceeded')
    return seconds


class HTTPStream:
    def __init__(self, connection, deadline):
        self.connection = connection
        self.response = None
        self.socket = None
        self.cancelled = threading.Event()
        self.timer = threading.Timer(remaining(deadline), self.abort)
        self.timer.daemon = True
        self.timer.start()

    def abort(self):
        self.cancelled.set()
        sock = self.socket or self.connection.sock
        if sock is not None:
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
        self.connection.close()

    def connect(self, address, deadline):
        # The OS resolver cannot be force-cancelled. Its daemon only returns
        # addresses; the bounded caller alone creates sockets and sends data.
        if self.cancelled.is_set():
            raise ObservationError('observer_connection_cancelled')
        answers = queue.Queue(maxsize=1)
        def resolve():
            try:
                answers.put(socket.getaddrinfo(*address, type=socket.SOCK_STREAM))
            except Exception:
                answers.put(None)
        threading.Thread(target=resolve, daemon=True).start()
        try:
            addresses = answers.get(timeout=remaining(deadline))
        except queue.Empty:
            raise ObservationError('observer_resolution_timeout') from None
        if not addresses or self.cancelled.is_set():
            raise ObservationError('observer_connection_cancelled')
        for family, socktype, protocol, _, target in addresses[:16]:
            if self.cancelled.is_set():
                break
            sock = socket.socket(family, socktype, protocol)
            self.socket = sock
            try:
                sock.settimeout(remaining(deadline))
                sock.connect(target)
                remaining(deadline)
                if self.cancelled.is_set():
                    raise ObservationError('observer_connection_cancelled')
                sock.settimeout(remaining(deadline))
                return sock
            except Exception:
                sock.close()
        raise ObservationError('observer_connection_failed')

    def close(self):
        self.timer.cancel()
        self.abort()
        if self.response is not None:
            self.response.close()

    def read(self):
        return self.response.read1(8192)


class HTTPSConnection(http.client.HTTPSConnection):
    def connect(self):
        if self.owner.cancelled.is_set():
            raise ObservationError('observer_connection_cancelled')
        http.client.HTTPConnection.connect(self)
        # Retain the SSLSocket before starting its blocking handshake. Otherwise
        # wrap_socket detaches the raw socket while a timer still owns only it.
        self.sock = self._context.wrap_socket(self.sock, server_hostname=self.host,
                                              do_handshake_on_connect=False)
        self.owner.socket = self.sock
        if self.owner.cancelled.is_set():
            raise ObservationError('observer_connection_cancelled')
        self.sock.settimeout(remaining(self.connection_deadline))
        self.sock.do_handshake()
        remaining(self.connection_deadline)
        if self.owner.cancelled.is_set():
            raise ObservationError('observer_connection_cancelled')


class ObserverHTTP:
    """http.client connects directly: no environment proxy or redirect handling."""
    def __init__(self, config):
        validate_config(config)
        self.url = urllib.parse.urlsplit(config['base_url'])
        self.token = read_bearer(config['bearer_token_file'])
        self.tls = tls_context(config.get('ca_file'))

    def open(self, path, deadline, sse=False):
        conn_type = HTTPSConnection if self.url.scheme == 'https' else http.client.HTTPConnection
        options = {'timeout': remaining(deadline)}
        if self.url.scheme == 'https':
            options['context'] = self.tls
        connection = conn_type(self.url.hostname, self.url.port, **options)
        stream = HTTPStream(connection, deadline)
        connection_deadline = min(deadline, time.monotonic() + 30)
        connection.owner = stream
        connection.connection_deadline = connection_deadline
        connection._create_connection = lambda address, *_args: stream.connect(address, connection_deadline)
        handshake = threading.Timer(remaining(connection_deadline), stream.abort)
        handshake.daemon = True
        handshake.start()
        try:
            connection.connect()
            remaining(connection_deadline)
            if stream.cancelled.is_set():
                raise ObservationError('observer_connection_cancelled')
            connection.request('GET', path, headers={'Authorization': 'Bearer ' + self.token,
                                                     'Accept': 'text/event-stream' if sse else 'application/json'})
            if stream.cancelled.is_set():
                raise ObservationError('observer_connection_cancelled')
            stream.socket = connection.sock or stream.socket
            stream.response = connection.getresponse()
            if stream.response.status != 200:
                raise ObservationError('observer_http_status')
            media = stream.response.getheader('Content-Type', '').split(';')[0].strip().lower()
            if media != ('text/event-stream' if sse else 'application/json'):
                raise ObservationError('observer_content_type')
            return stream
        except Exception:
            stream.close()
            raise ObservationError('observer_transport_failed') from None
        finally:
            handshake.cancel()

    def get(self, path, deadline):
        stream = self.open(path, deadline)
        try:
            body = bytearray()
            while True:
                remaining(deadline)
                part = stream.read()
                if not part:
                    break
                body.extend(part)
                if len(body) > 1 << 20:
                    raise ObservationError('observer_body_limit')
            value = json.loads(body)
            if not isinstance(value, dict):
                raise ValueError()
            return value
        except Exception:
            raise ObservationError('observer_response_invalid') from None
        finally:
            stream.close()


class Frames:
    def __init__(self):
        self.pending = bytearray()
        self.lines = []
        self.frame_bytes = self.total_bytes = self.count = 0

    def feed(self, chunk):
        self.total_bytes += len(chunk)
        if self.total_bytes > 8 << 20:
            raise ObservationError('stream_byte_limit')
        self.pending.extend(chunk)
        frames = []
        while b'\n' in self.pending:
            line, _, tail = self.pending.partition(b'\n')
            self.pending = bytearray(tail)
            self.frame_bytes += len(line) + 1
            if self.frame_bytes > 65536:
                raise ObservationError('stream_frame_limit')
            line = line.rstrip(b'\r')
            if line:
                self.lines.append(line.decode('utf-8', errors='strict'))
            else:
                self.count += 1
                if self.count > 10000:
                    raise ObservationError('stream_frame_count_limit')
                frames.append(self.lines)
                self.lines = []
                self.frame_bytes = 0
        if self.frame_bytes + len(self.pending) > 65536:
            raise ObservationError('stream_frame_limit')
        return frames


class Listener:
    def __init__(self, transport, scope, deadline):
        self.scope, self.deadline = scope, deadline
        self.stream = transport.open('/api/v1/events/stream/', deadline, sse=True)
        self.condition = threading.Condition()
        self.connected = False
        self.failure = None
        self.closed = False
        self.candidates = {}
        self.thread = threading.Thread(target=self.read, daemon=True)
        self.thread.start()

    def frame(self, lines, arrival):
        data = []
        event = ''
        for line in lines:
            field, _, value = line.partition(':')
            value = value.removeprefix(' ')
            if field == 'data': data.append(value)
            if field == 'event': event = value
        if not data:
            if ': connected' in lines:
                self.connected = True
            return
        if event not in ('', 'message'):
            return
        envelope = json.loads('\n'.join(data))
        if not isinstance(envelope, dict):
            raise ObservationError('stream_envelope_invalid')
        if envelope.get('type') != 'cluster.k8s_changed':
            return
        body = envelope.get('data')
        if not isinstance(body, dict) or any(body.get(key) != value for key, value in self.scope.items()):
            return
        rv = body.get('resource_version')
        if not isinstance(rv, str) or not 1 <= len(rv) <= 256:
            raise ObservationError('stream_resource_version_invalid')
        if rv not in self.candidates:
            if len(self.candidates) >= 256:
                raise ObservationError('stream_candidate_limit')
            self.candidates[rv] = arrival

    def read(self):
        parser = Frames()
        try:
            while not self.closed:
                remaining(self.deadline)
                chunk = self.stream.read()
                arrival = time.monotonic_ns()
                if not chunk:
                    raise ObservationError('stream_disconnected')
                frames = parser.feed(chunk)
                with self.condition:
                    for lines in frames:
                        self.frame(lines, arrival)
                    self.condition.notify_all()
        except Exception as error:
            with self.condition:
                self.failure = failure_code(error)
                self.condition.notify_all()

    def wait(self, deadline, rv=None, start_ns=0):
        with self.condition:
            while True:
                if self.failure:
                    raise ObservationError(self.failure)
                if rv is None and self.connected:
                    return None
                if rv in self.candidates and self.candidates[rv] >= start_ns:
                    arrival = self.candidates[rv]
                    if arrival / 1e9 <= deadline:
                        return arrival
                self.condition.wait(timeout=remaining(deadline))

    def close(self):
        self.closed = True
        self.stream.close()
        self.thread.join(timeout=2)
        if self.thread.is_alive():
            raise ObservationError('stream_shutdown_failed')


def verify_mapping(transport, ledger, config, deadline):
    ledger.check_identity()
    prefix = '/api/v1/clusters/' + config['cluster_id'] + '/k8s/api/v1/namespaces/'
    for name, uid in (('kube-system', ledger.data['identity']['cluster_uid']),
                      (ledger.runner.manifest['namespace'], ledger.data['identity']['namespace_uid'])):
        obj = transport.get(prefix + name, deadline)
        if obj.get('apiVersion') != 'v1' or obj.get('kind') != 'Namespace' or obj.get('metadata', {}).get('name') != name or obj['metadata'].get('uid') != uid:
            raise ObservationError('cluster_mapping_mismatch')


def settled(obj):
    try:
        spec, status, md = obj['spec'], obj['status'], obj['metadata']
        desired = spec['replicas']
        if type(desired) is not int or not 1 <= desired <= 100 or md.get('deletionTimestamp'):
            return False
        if type(md['generation']) is not int or status['observedGeneration'] != md['generation']:
            return False
        return all(status.get(key, 0) == desired for key in ('replicas', 'updatedReplicas', 'availableReplicas', 'readyReplicas')) and status.get('unavailableReplicas', 0) == 0
    except (KeyError, TypeError):
        return False


def summarize(result):
    trials = result['trials']
    result['requested'] = len(trials)
    result['attempted'] = sum(t['status'] != 'not_run' for t in trials)
    for status in ('succeeded', 'timeout', 'missed', 'error', 'not_run'):
        result[status] = sum(t['status'] == status for t in trials)
    result['p95_ms'] = None
    if len(trials) >= 20 and all(t['status'] == 'succeeded' for t in trials):
        ordered = sorted(t['latency_ms'] for t in trials)
        result['p95_ms'] = ordered[math.ceil(.95 * len(ordered)) - 1]
    result['status'] = 'complete' if result['succeeded'] == len(trials) else 'partial'


def observe(runner, step, transport_factory=ObserverHTTP, listener_factory=Listener):
    """One controlled step; cleanup may safely drain beyond its measurement deadline."""
    deadline = time.monotonic() + step.get('timeout_seconds', 600)
    result = {'measurement': 'mutation_dispatch_to_sse', 'trials': [
        {'index': index, 'status': 'not_run', 'code': 'not_attempted'} for index in range(step['repetitions'])]}
    runner.report.setdefault('observations', []).append(result)
    summarize(result)
    runner.checkpoint()
    try:
        transport = transport_factory(runner.manifest['observer'])
    except Exception as error:
        result['setup_code'] = failure_code(error)
        runner.checkpoint()
        raise ObservationError('observer_setup_failed') from None
    config = runner.manifest['observer']
    ledger = runner.ledger
    old_deadline = getattr(runner.kubectl, 'deadline', None)
    for trial in result['trials']:
        if time.monotonic() >= deadline:
            break
        trial.update(status='error', code='trial_incomplete')
        listener = None
        operation = None
        first_operation = len(ledger.data['operations'])
        runner.kubectl.deadline = deadline
        try:
            verify_mapping(transport, ledger, config, deadline)
            before = runner.owned('deployment', step['name'])
            if not settled(before):
                raise ObservationError('deployment_not_settled')
            if remaining(deadline) < 2:
                raise ObservationError('deadline_exceeded')
            time.sleep(2)
            current = runner.owned('deployment', step['name'])
            if not settled(current) or any(before['metadata'].get(k) != current['metadata'].get(k) for k in ('uid', 'resourceVersion')):
                raise ObservationError('deployment_not_quiet')
            trial_deadline = min(deadline, time.monotonic() + 120)
            scope = {'cluster_id': config['cluster_id'], 'kind': 'Deployment', 'api_group': 'apps',
                     'api_version': 'v1', 'namespace': runner.manifest['namespace'], 'name': step['name']}
            listener = listener_factory(transport, scope, trial_deadline)
            listener.wait(min(trial_deadline, time.monotonic() + 30))
            runner.kubectl.deadline = trial_deadline
            operation = ledger.observe_annotation(step['name'], current, trial_deadline)
            trial['ledger_operation'] = len(ledger.data['operations']) - 1
            start = operation['observation']['dispatch_ns']
            arrival = listener.wait(trial_deadline, operation['observation']['resource_version'], start)
            verified = ledger.get(operation)
            if verified is None or ledger.value(verified, 'observation') != operation['expected']:
                raise ObservationError('observation_state_unverified')
            remaining(trial_deadline)
            operation['observation']['arrival_ns'] = arrival
            ledger.save()
            trial.update(status='succeeded', code='exact_event_and_state', latency_ms=(arrival - start) / 1e6)
        except Exception as error:
            code = failure_code(error) if isinstance(error, ObservationError) else 'mutation_or_verification_failed'
            trial.update(status='timeout' if code == 'deadline_exceeded' else 'error', code=code)
            if code == 'deadline_exceeded' and operation is not None:
                trial.update(status='missed', code='exact_event_unobserved')
        finally:
            runner.kubectl.deadline = old_deadline
            cleanup_ok = True
            try:
                if listener is not None:
                    listener.close()
            except Exception:
                trial.update(status='error', code='stream_shutdown_failed')
            # Include an intent whose mutation raised before it returned a receipt.
            for op in reversed(ledger.data['operations'][first_operation:]):
                try:
                    ledger.cleanup_one(op)
                except Exception:
                    op['state'] = 'unresolved'
                    ledger.save()
                    cleanup_ok = False
                    break
            trial['cleanup_status'] = 'passed' if cleanup_ok else 'unresolved'
            if not cleanup_ok:
                trial.update(status='error', code='cleanup_unresolved')
            if time.monotonic() > deadline and trial['status'] == 'succeeded':
                trial.update(status='timeout', code='step_deadline_exceeded')
            summarize(result)
            runner.checkpoint()
        if not cleanup_ok or trial['status'] not in ('succeeded', 'missed'):
            break
    summarize(result)
    runner.checkpoint()
    if result['status'] != 'complete':
        raise ObservationError('observation_step_incomplete')
