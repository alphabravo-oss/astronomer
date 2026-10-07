"""Narrow, resumable mutation ledger for run-delivery-resilience-drill.

No templates, environment, credentials or resource bodies are persisted here.
"""
import copy
import datetime
import re
import fcntl
import hashlib
import stat
import json
import os
import time
import uuid
from pathlib import Path

LOCK_ROOT = '/tmp'

OWNER = 'delivery.astronomer.io/qualification-run'
DISPOSABLE = 'delivery.astronomer.io/disposable'
NONCE = 'delivery.astronomer.io/qualification-operation'
OBSERVATION = 'delivery.astronomer.io/qualification-observation'
RESTART = 'kubectl.kubernetes.io/restartedAt'
RESOURCES = {
    'deployment': ('apps/v1', 'Deployment', 'deployments'),
    'statefulset': ('apps/v1', 'StatefulSet', 'statefulsets'),
    'pod': ('v1', 'Pod', 'pods'),
    'job': ('batch/v1', 'Job', 'jobs'),
    'networkpolicy': ('networking.k8s.io/v1', 'NetworkPolicy', 'networkpolicies'),
    'configmap': ('v1', 'ConfigMap', 'configmaps'),
}


class LedgerError(RuntimeError):
    pass


def escape(key):
    return key.replace('~', '~0').replace('/', '~1')


def restart_value(value):
    if value is None:
        return True
    if not isinstance(value, str) or not re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})', value):
        return False
    try:
        datetime.datetime.fromisoformat(value.replace('Z', '+00:00'))
        return True
    except ValueError:
        return False


def private_read(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode) or st.st_mode & 0o077 or st.st_uid != os.getuid() or st.st_size > 4 << 20:
            raise LedgerError('ledger must be a private bounded regular file')
        with os.fdopen(fd, 'rb', closefd=False) as f:
            return json.loads(f.read((4 << 20) + 1))
    finally:
        os.close(fd)


class Ledger:
    def __init__(self, runner, resume=False):
        self.runner = runner
        self.api = runner.kubectl
        self.path = Path(str(runner.evidence_path) + '.ledger.json')
        self.resume = resume
        self.fd = None
        self.data = None

    def __enter__(self):
        identity = self.identity()
        directory = Path(LOCK_ROOT) / ('astronomer-drill-locks-' + str(os.getuid()))
        directory.mkdir(mode=0o700, exist_ok=True)
        st = directory.lstat()
        if not stat.S_ISDIR(st.st_mode) or st.st_uid != os.getuid() or st.st_mode & 0o077:
            raise LedgerError('unsafe local lock directory')
        key = hashlib.sha256((identity['cluster_uid'] + ':' + identity['namespace_uid']).encode()).hexdigest()
        self.fd = os.open(directory / key, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        try:
            st = os.fstat(self.fd)
            if not stat.S_ISREG(st.st_mode) or st.st_uid != os.getuid() or st.st_mode & 0o077:
                raise LedgerError('invalid local ledger lock')
            fcntl.flock(self.fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            if self.identity() != identity:
                raise LedgerError('identity changed during lock acquisition')
            if self.resume:
                self.data = private_read(self.path)
                if self.data.get('identity') != identity or self.data.get('schema') != 'astronomer-drill-ledger/v1':
                    raise LedgerError('ledger identity mismatch')
                self.validate()
            else:
                if self.path.exists() or self.path.is_symlink() or self.runner.evidence_path.exists():
                    raise LedgerError('existing evidence requires explicit resume-cleanup')
                self.data = {'schema': 'astronomer-drill-ledger/v1', 'identity': identity,
                             'operations': [], 'cleanup_complete': False, 'source_digest': self.runner.report.get('source_digest')}
                self.save()
            return self
        except BaseException:
            os.close(self.fd)
            self.fd = None
            raise

    def __exit__(self, *args):
        if self.fd is not None:
            os.close(self.fd)

    def identity(self):
        ns = self.api.get('namespace', self.runner.manifest['namespace'])
        self.runner.require_owned(ns)
        system = self.api.get('namespace', 'kube-system')
        nsuid = ns.get('metadata', {}).get('uid')
        clusteruid = system.get('metadata', {}).get('uid')
        if any(not isinstance(value, str) or not 1 <= len(value) <= 256 for value in (nsuid, clusteruid)):
            raise LedgerError('cluster or namespace UID unavailable')
        with open('/etc/machine-id', 'rb') as handle:
            host = handle.read(256).strip()
        if not re.fullmatch(b'[0-9a-fA-F]{32}', host):
            raise LedgerError('host recovery identity unavailable')
        return {'cluster_uid': clusteruid, 'namespace_uid': nsuid,
                'namespace': self.runner.manifest['namespace'], 'context': self.runner.manifest['context'],
                'run_id': self.runner.run_id, 'manifest_digest': self.runner.report['manifest_digest'],
                'recovery_host': hashlib.sha256(host).hexdigest(),
                'ledger_location': hashlib.sha256(str(self.path.resolve()).encode()).hexdigest()}

    def validate(self):
        ops = self.data.get('operations')
        if not isinstance(ops, list) or len(ops) > 10001:
            raise LedgerError('invalid ledger operations')
        for op in ops:
            if not isinstance(op, dict):
                raise LedgerError('invalid ledger operation')
            if op.get('kind') not in RESOURCES or op.get('namespace') != self.runner.manifest['namespace'] or op.get('mode') not in ('create', 'field', 'delete'):
                raise LedgerError('invalid ledger operation')
            if op.get('api_version') != RESOURCES[op['kind']][0] or op.get('gvk_kind') != RESOURCES[op['kind']][1]:
                raise LedgerError('invalid ledger GVK')
            self.runner.bounded_name(op.get('name'))
            if not isinstance(op.get('nonce'), str) or not re.fullmatch('[0-9a-f]{32}', op['nonce']):
                raise LedgerError('invalid operation nonce')
            if op.get('uid') is not None and (not isinstance(op['uid'], str) or not 1 <= len(op['uid']) <= 256):
                raise LedgerError('invalid operation UID')
            if op['mode'] == 'field' and (op['kind'] not in ('deployment', 'statefulset') or op.get('field') not in ('replicas', 'restart', 'observation')):
                raise LedgerError('invalid field operation')
            if op['mode'] == 'delete' and op['kind'] != 'pod':
                raise LedgerError('invalid deletion operation')
            if op['mode'] == 'create' and op['kind'] not in ('configmap', 'networkpolicy', 'job'):
                raise LedgerError('invalid creation operation')
            if op.get('field') not in (None, 'replicas', 'restart', 'observation'):
                raise LedgerError('invalid ledger field')
            if op.get('mode') == 'field':
                for key in ('original', 'expected'):
                    value = op.get(key)
                    if op['field'] == 'replicas' and (type(value) is not int or not 0 <= value <= 100):
                        raise LedgerError('invalid replica restoration')
                    if op['field'] == 'observation' and (op['kind'] != 'deployment' or (value is not None and (not isinstance(value, str) or not re.fullmatch('q-[0-9a-f]{32}', value)))):
                        raise LedgerError('invalid observation restoration')
                    if op['field'] == 'restart' and not restart_value(value):
                        raise LedgerError('invalid restart restoration')
            if op.get('state') not in ('intent', 'mutated', 'cleaning', 'done', 'unresolved'):
                raise LedgerError('invalid ledger state')

    def save(self):
        if len(json.dumps(self.data).encode()) > 4 << 20:
            raise LedgerError('ledger size limit exceeded')
        self.runner.write_atomic(self.path, self.data)

    def check_identity(self):
        if self.identity() != self.data['identity']:
            raise LedgerError('cluster or namespace identity changed')

    def get(self, op):
        obj = self.api.get_optional(op['kind'], op['name'])
        if obj is None:
            return None
        return self.validate_object(op, obj)

    def validate_object(self, op, obj):
        self.runner.require_owned(obj)
        md = obj.get('metadata', {})
        if md.get('name') != op['name'] or md.get('namespace') != op['namespace'] or obj.get('apiVersion') != op['api_version'] or obj.get('kind') != op['gvk_kind']:
            raise LedgerError('resource scope or GVK changed')
        if not md.get('uid') or not md.get('resourceVersion'):
            raise LedgerError('resource identity unavailable')
        if op.get('uid') and md['uid'] != op['uid']:
            raise LedgerError('resource UID replaced')
        if op['mode'] == 'create' and md.get('labels', {}).get(NONCE) != op['nonce']:
            raise LedgerError('creation nonce mismatch')
        return obj

    def intent(self, mode, kind, name, obj=None, **fields):
        self.check_identity()
        if len(self.data['operations']) >= 10001:
            raise LedgerError('ledger operation limit exceeded')
        api, gvk, _ = RESOURCES[kind]
        op = dict(mode=mode, kind=kind, api_version=api, gvk_kind=gvk,
                  namespace=self.runner.manifest['namespace'], name=name,
                  nonce=uuid.uuid4().hex, uid=None, state='intent', **fields)
        if obj is not None:
            self.runner.require_owned(obj)
            md = obj.get('metadata', {})
            if not md.get('uid') or not md.get('resourceVersion'):
                raise LedgerError('resource UID/version unavailable')
            op['uid'] = md['uid']
            op['original_resource_version'] = md['resourceVersion']
        self.data['operations'].append(op)
        self.save()
        return op

    def lock(self):
        ops = self.data['operations']
        if self.resume:
            if self.data['cleanup_complete']:
                if self.api.get_optional('configmap', 'qualification-drill-lock') is not None:
                    raise LedgerError('completed cleanup lock replaced or reappeared')
                return
            if not ops:
                if self.api.get_optional('configmap', 'qualification-drill-lock') is not None:
                    raise LedgerError('unowned cluster lock exists')
                self.data['cleanup_complete'] = True
                self.save()
                return
            if not ops or not ops[0].get('lock'):
                raise LedgerError('lock intent unavailable')
            lock = ops[0]
            if lock['mode'] != 'create' or lock['kind'] != 'configmap' or lock['name'] != 'qualification-drill-lock':
                raise LedgerError('invalid cluster lock intent')
            obj = self.get(lock)
            if obj is None:
                if all(op['state'] == 'done' for op in ops[1:]) and lock['state'] in ('cleaning', 'done'):
                    lock['state'] = 'done'
                    self.save()
                    return
                raise LedgerError('cluster lock missing; manual resolution required')
            lock['uid'] = obj['metadata']['uid']
            self.save()
            return
        self.create('configmap', 'qualification-drill-lock', {'apiVersion': 'v1', 'kind': 'ConfigMap'}, lock=True)

    def create(self, kind, name, body, lock=False):
        # Do not even record a cleanup intent for an already existing object.
        if self.api.get_optional(kind, name) is not None:
            raise LedgerError('create refused: resource already exists')
        op = self.intent('create', kind, name, lock=lock)
        document = copy.deepcopy(body)
        document['metadata'] = {'name': name, 'namespace': op['namespace'],
                                'labels': {OWNER: self.runner.run_id, DISPOSABLE: 'true', NONCE: op['nonce']}}
        try:
            response = self.api.run(['create', '-f', '-', '-o', 'json'], stdin=document)
        except Exception:
            # Only an exact persisted nonce can resolve an ambiguous create.
            obj = self.get(op)
            if obj is None:
                raise LedgerError('ambiguous creation unresolved') from None
        else:
            # A successful response is the authoritative UID receipt. Persist it
            # before the verification GET, which could observe a replacement.
            obj = self.validate_object(op, json.loads(response))
        op['uid'] = obj['metadata']['uid']
        self.save()
        if self.get(op) is None:
            raise LedgerError('creation verification unavailable')
        op['state'] = 'mutated'
        self.save()
        return op

    def value(self, obj, field):
        if field == 'observation':
            return obj.get('metadata', {}).get('annotations', {}).get(OBSERVATION)
        if field == 'replicas':
            return obj.get('spec', {}).get('replicas')
        return obj.get('spec', {}).get('template', {}).get('metadata', {}).get('annotations', {}).get(RESTART)

    def patch_field(self, op, obj, value):
        md = obj['metadata']
        patch = [{'op': 'test', 'path': '/metadata/uid', 'value': op['uid']},
                 {'op': 'test', 'path': '/metadata/resourceVersion', 'value': md['resourceVersion']}]
        if op['field'] == 'replicas':
            patch.append({'op': 'replace', 'path': '/spec/replicas', 'value': value})
        else:
            prefix = '/metadata' if op['field'] == 'observation' else '/spec/template/metadata'
            template_md = obj['metadata'] if op['field'] == 'observation' else obj['spec']['template']['metadata']
            if 'annotations' not in template_md:
                patch.append({'op': 'add', 'path': prefix + '/annotations', 'value': {}})
            path = prefix + '/annotations/' + escape(OBSERVATION if op['field'] == 'observation' else RESTART)
            if value is None:
                patch.append({'op': 'remove', 'path': path})
            else:
                patch.append({'op': 'add', 'path': path, 'value': value})
        args = ['patch', op['kind'], op['name'], '--type=json', '--patch-file=/dev/stdin']
        if op['field'] == 'observation':
            args += ['-o', 'json']
        return self.api.run(args, stdin=patch)

    def observe_annotation(self, name, settled_object, deadline):
        original = self.value(settled_object, 'observation')
        if original is not None and (not isinstance(original, str) or not re.fullmatch('q-[0-9a-f]{32}', original)):
            raise LedgerError('invalid original observation annotation')
        expected = 'q-' + uuid.uuid4().hex
        op = self.intent('field', 'deployment', name, settled_object,
                         field='observation', original=original, expected=expected, observation={})
        # Use the settled read's RV, never a newer read that could hide a change.
        current = self.validate_object(op, settled_object)
        if time.monotonic() >= deadline:
            op['state'] = 'done'  # No request was sent.
            self.save()
            raise LedgerError('observation deadline exceeded before mutation')
        dispatch = time.monotonic_ns()
        raw = self.patch_field(op, current, expected)
        receipt = self.validate_object(op, json.loads(raw))
        rv = receipt['metadata']['resourceVersion']
        if self.value(receipt, 'observation') != expected or not isinstance(rv, str) or not 1 <= len(rv) <= 256 or rv == current['metadata']['resourceVersion']:
            raise LedgerError('observation receipt invalid')
        op['acknowledged'] = True
        op['observation'] = {'dispatch_ns': dispatch, 'resource_version': rv}
        op['state'] = 'mutated'
        self.save()
        return op

    def change(self, kind, name, field, expected):
        obj = self.runner.owned(kind, name)
        original = self.value(obj, field)
        if field == 'replicas' and (type(original) is not int or not 0 <= original <= 100):
            raise LedgerError('invalid original replicas')
        if field == 'restart' and not restart_value(original):
            raise LedgerError('invalid original restart annotation')
        op = self.intent('field', kind, name, obj, field=field, original=original, expected=expected)
        if original == expected:
            op['state'] = 'done'
            self.save()
            return op
        current = self.get(op)
        if current is None or self.value(current, field) != original:
            raise LedgerError('mutation field changed externally')
        self.patch_field(op, current, expected)
        op['acknowledged'] = True
        self.save()
        current = self.get(op)
        if current is None or self.value(current, field) != expected:
            raise LedgerError('mutation verification failed')
        op['state'] = 'mutated'
        self.save()
        return op

    def delete(self, op, obj):
        _, _, plural = RESOURCES[op['kind']]
        api = op['api_version']
        prefix = '/api/' if api == 'v1' else '/apis/'
        path = prefix + api + '/namespaces/' + op['namespace'] + '/' + plural + '/' + op['name']
        options = {'apiVersion': 'v1', 'kind': 'DeleteOptions',
                   'preconditions': {'uid': obj['metadata']['uid'], 'resourceVersion': obj['metadata']['resourceVersion']},
                   'propagationPolicy': 'Foreground'}
        self.api.run(['delete', '--raw=' + path, '-f', '-'], stdin=options)

    def delete_pod(self, pod):
        op = self.intent('delete', 'pod', pod['metadata']['name'], pod, irreversible=True)
        self.delete(op, self.get(op))
        op['state'] = 'mutated'
        self.save()
        return op

    def cleanup_one(self, op):
        if op['state'] == 'done':
            return
        self.check_identity()
        obj = self.get(op)
        if op['mode'] == 'field':
            if obj is None:
                raise LedgerError('restore target missing')
            value = self.value(obj, op['field'])
            if value == op['original'] and not (op.get('acknowledged') or op.get('restore_started')):
                raise LedgerError('ambiguous field mutation unresolved')
            if value != op['original']:
                if value != op['expected']:
                    raise LedgerError('restore field changed externally')
                op['state'] = 'cleaning'
                op['restore_started'] = True
                self.save()
                self.patch_field(op, obj, op['original'])
            verified = self.get(op)
            if verified is None or self.value(verified, op['field']) != op['original']:
                raise LedgerError('restore verification failed')
        else:
            if obj is None and op['mode'] == 'create' and not op.get('uid'):
                raise LedgerError('ambiguous creation unresolved')
            if obj is not None:
                op['uid'] = obj['metadata']['uid']
                op['state'] = 'cleaning'
                self.save()
                self.delete(op, obj)
                deadline = time.monotonic() + 60
                while self.get(op) is not None:
                    if time.monotonic() >= deadline:
                        raise LedgerError('deletion verification timeout')
                    time.sleep(0.2)
        op['state'] = 'done'
        self.save()

    def cleanup(self):
        failed = False
        for op in reversed(self.data['operations']):
            # A failed later restore may still depend on an earlier altered
            # value; retain the lock and all earlier operations for resumption.
            try:
                self.cleanup_one(op)
            except Exception:
                op['state'] = 'unresolved'
                self.save()
                failed = True
                break
        self.data['cleanup_complete'] = not failed
        self.save()
        return not failed
