"""Owned synthetic cost lane; originals never overwritten, no credential output."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import sys
import time
import threading
from urllib.parse import parse_qsl, unquote, urlsplit, urlunsplit

ROOT = Path('/root/astronomer-all/astronomer-032-w02-current')
NAMESPACE = 'plan032-recovery'
NAMESPACE_UID = '0883a483-0daf-4ee0-9b69-609a203cb767'
OWNER = 'plan032-20261009'
CLUSTERS = {17: ('plan032-postgres', '2f83aaff-dbbf-40ef-8880-542225fec0b4', 'debb98e4-3427-4a3f-8166-55b3343e39f5', 30432), 18: ('plan032-postgres18', 'ea0f410a-e1ea-4cb4-9fe5-40a35a020d3e', '42caa8ab-f2f6-4a1b-9b61-c7876fd3b141', 30433)}
ARGV = ['/usr/local/go/bin/go', 'test', '-count=1', '-json', '-run', '^TestControlPlaneCursorPostgresRepresentativeSelectiveCost$', './internal/handler']
KUBECTL = ['/root/.cache/astronomer-032/toolchains/bin/kubectl-1.37.1', '--kubeconfig', '/root/.cache/astronomer-032/qualification/cnpg36/kubeconfig', '--request-timeout=10s']

def get(kind, name=None):
    command = KUBECTL + ([] if kind in ('namespace', 'node') else ['-n', NAMESPACE]) + ['get', kind] + ([name] if name else []) + ['-o', 'json']
    return json.loads(subprocess.check_output(command, stderr=subprocess.DEVNULL, timeout=15))

def check_ownership(major, address, namespace, cluster, primary, services, endpoints, node):
    name, uid, service_uid, port = CLUSTERS[major]
    ns = namespace['metadata']
    assert ns['name'] == NAMESPACE and ns['uid'] == NAMESPACE_UID and ns.get('labels', {}).get('astronomer.io/owner') == OWNER and namespace['status']['phase'] == 'Active'
    owner = cluster['metadata']
    assert owner['name'] == name and owner['namespace'] == NAMESPACE and owner['uid'] == uid and owner.get('labels', {}).get('astronomer.io/owner') == OWNER and cluster['status']['readyInstances'] == 3
    pod = primary['metadata']
    controllers = [ref for ref in pod.get('ownerReferences', []) if ref.get('controller') is True]
    assert pod['namespace'] == NAMESPACE and pod['name'] == cluster['status']['currentPrimary'] and pod['labels'].get('cnpg.io/cluster') == name and pod['labels'].get('cnpg.io/instanceRole') == 'primary'
    assert len(controllers) == 1 and controllers[0]['kind'] == 'Cluster' and controllers[0]['uid'] == uid and controllers[0]['apiVersion'] == 'postgresql.cnpg.io/v1'
    assert any(condition.get('type') == 'Ready' and condition.get('status') == 'True' for condition in primary['status'].get('conditions', []))
    matching = [service for service in services['items'] if any(p.get('nodePort') == address.port for p in service['spec'].get('ports', []))]
    assert address.port == port and len(matching) == 1
    service = matching[0]
    assert service['metadata']['namespace'] == NAMESPACE and service['metadata']['uid'] == service_uid and service['spec']['type'] == 'NodePort' and service['spec']['selector'] == {'cnpg.io/cluster': name, 'cnpg.io/instanceRole': 'primary'}
    assert len(service['spec']['ports']) == 1 and service['spec']['ports'][0]['port'] == 5432 and service['spec']['ports'][0]['targetPort'] == 5432 and service['spec']['ports'][0].get('protocol', 'TCP') == 'TCP'
    assert endpoints['metadata']['name'] == service['metadata']['name'] and endpoints['metadata']['namespace'] == NAMESPACE
    assert len(endpoints.get('subsets', [])) == 1
    subset = endpoints['subsets'][0]
    assert not subset.get('notReadyAddresses') and len(subset.get('addresses', [])) == 1 and len(subset.get('ports', [])) == 1 and subset['ports'][0]['port'] == 5432 and subset['ports'][0].get('protocol', 'TCP') == 'TCP'
    target = subset['addresses'][0]
    assert target['targetRef']['kind'] == 'Pod' and target['targetRef']['namespace'] == NAMESPACE and target['targetRef']['uid'] == pod['uid'] and target['targetRef']['name'] == pod['name'] and target['ip'] == primary['status']['podIP']
    assert node['metadata']['name'] == target['nodeName'] and [a['address'] for a in node['status']['addresses'] if a.get('type') == 'InternalIP'] == [address.hostname]
    return {'namespace_uid': ns['uid'], 'cluster': name, 'cluster_uid': uid, 'primary': pod['name'], 'pod_uid': pod['uid'], 'service': service['metadata']['name'], 'service_uid': service_uid, 'endpoint_uid': target['targetRef']['uid'], 'node_uid': node['metadata']['uid'], 'ready_instances': 3, 'node_host_verified': True}

def private_read(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        info = os.fstat(descriptor)
        assert stat.S_ISREG(info.st_mode) and info.st_uid == os.geteuid() and stat.S_IMODE(info.st_mode) == 0o600
        raw = os.read(descriptor, 8193)
        assert len(raw) <= 8192
        return json.loads(raw)
    finally: os.close(descriptor)

def private_write(source, major, database_url, root=Path('/root/.cache/astronomer-032/qualification')):
    assert re.fullmatch('[a-f0-9]{40}', source)
    parent = root / ('w07-cost-' + source[:8])
    parent.mkdir(mode=0o700, exist_ok=True)
    directory = os.open(parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        info = os.fstat(directory)
        assert info.st_uid == os.geteuid() and stat.S_IMODE(info.st_mode) == 0o700
        filename = 'postgres' + str(major) + '-database.json'
        descriptor = os.open(filename, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=directory)
        with os.fdopen(descriptor, 'w') as dest:
            opened = os.fstat(dest.fileno())
            json.dump({'database_url': database_url}, dest)
            dest.flush()
            os.fsync(dest.fileno())
        final = os.stat(filename, dir_fd=directory, follow_symlinks=False)
        assert stat.S_ISREG(final.st_mode) and final.st_ino == opened.st_ino and final.st_dev == opened.st_dev and final.st_uid == os.geteuid() and stat.S_IMODE(final.st_mode) == 0o600
        return str(parent / filename)
    finally: os.close(directory)

def live_fence(major, schema, database_url=None):
    name = CLUSTERS[major][0]
    if database_url is None:
        template = Path('/root/.cache/astronomer-032/qualification/cnpg36/w06-category-database.json') if major == 17 else Path('/tmp/w07-cnpg/database18.json')
        address = urlsplit(private_read(template)['database_url'])._replace(path='/plan032_w07_cursor' + str(major))
        database_url = urlunsplit(address)
    address = urlsplit(database_url)
    assert address.scheme in ('postgres', 'postgresql') and address.path == '/plan032_w07_cursor' + str(major) and not address.fragment
    options = parse_qsl(address.query, keep_blank_values=True, strict_parsing=True)
    assert len({key for key, _ in options}) == len(options) and all(key in ('sslmode', 'sslrootcert', 'connect_timeout', 'application_name') for key, _ in options)
    cluster = get('cluster', name)
    primary = get('pod', cluster['status']['currentPrimary'])
    services = get('services')
    matching = [service for service in services['items'] if any(p.get('nodePort') == address.port for p in service['spec'].get('ports', []))]
    assert len(matching) == 1
    endpoints = get('endpoints', matching[0]['metadata']['name'])
    targets = [a for subset in endpoints.get('subsets', []) for a in subset.get('addresses', [])]
    assert len(targets) == 1
    safe = check_ownership(major, address, get('namespace', NAMESPACE), cluster, primary, services, endpoints, get('node', targets[0]['nodeName']))
    env = dict(os.environ, PGHOST=address.hostname, PGPORT=str(address.port), PGDATABASE=address.path.lstrip('/'), PGUSER=unquote(address.username), PGPASSWORD=unquote(address.password), PGCONNECT_TIMEOUT='5')
    for key, value in options:
        if key == 'sslmode': env['PGSSLMODE'] = value
    connected = subprocess.run(['psql', '-X', '-At', '-v', 'ON_ERROR_STOP=1', '-c', "SELECT current_setting('server_version_num')::int/10000,current_database(),version,dirty FROM schema_migrations"], env=env, capture_output=True, text=True, timeout=15)
    assert connected.returncode == 0 and connected.stdout.strip().split('|') == [str(major), 'plan032_w07_cursor' + str(major), str(schema), 'f'], 'connected identity mismatch; details suppressed'
    safe.update(server_major=major, schema=schema, dirty=False)
    return database_url, safe

def expected_schema_version(root):
    matches = re.findall(r'^const[ \t]+ExpectedSchemaVersion[ \t]+int64[ \t]*=[ \t]*([0-9]+)[ \t]*$', (root / 'internal/db/schema_version.go').read_text(), re.MULTILINE)
    assert len(matches) == 1, 'canonical typed schema declaration is missing or ambiguous'
    return int(matches[0])

def fingerprint(root):
    return json.loads(subprocess.check_output([sys.executable, 'scripts/hash-source-tree.py', '--root', str(root)], cwd=root, timeout=60))

def active_owned_group(group):
    for entry in Path('/proc').iterdir():
        if not entry.name.isdigit(): continue
        try:
            tail = (entry / 'stat').read_text().rsplit(')', 1)[1].split()
            if tail[0] != 'Z' and int(tail[2]) == group: return True
        except (FileNotFoundError, ProcessLookupError, PermissionError): continue
    return False

def stop_owned_group(child, grace=5):
    if child is None: return True
    for action in (signal.SIGTERM, signal.SIGKILL):
        try: os.killpg(child.pid, action)
        except ProcessLookupError: pass
        deadline = time.monotonic() + grace
        while time.monotonic() < deadline:
            child.poll()
            if child.returncode is not None and not active_owned_group(child.pid): return True
            time.sleep(0.02)
    child.poll()
    return child.returncode is not None and not active_owned_group(child.pid)

def run_lane(command, env, log, guard, ownership, source_identity, timeout=600, interval=10, grace=5):
    started = time.time()
    child = None
    failure = None
    joined = True
    receipt = {'argv': command, 'exit': None, 'failure_category': None, 'source_before': None, 'source_after': None, 'final_guard_pass': False}
    try:
        guard('preflight')
        receipt['source_before'] = source_identity()
        ownership()  # Re-fence immediately before any owned child can run.
        guard('before-process')
        with log.open('x') as dest:
            child = subprocess.Popen(command, cwd=ROOT, env=env, stdout=dest, stderr=subprocess.STDOUT, start_new_session=True)
            deadline = time.monotonic() + timeout
            while child.poll() is None:
                if time.monotonic() >= deadline:
                    failure = 'owned_process_timeout'
                    break
                guard('maintained')
                try: child.wait(timeout=min(interval, max(0.01, deadline - time.monotonic())))
                except subprocess.TimeoutExpired: pass
            if failure is None and active_owned_group(child.pid): failure = 'owned_group_leftover'
    except BaseException as error:
        failure = failure or type(error).__name__
    finally:
        handlers = {}
        if threading.current_thread() is threading.main_thread():
            for number in (signal.SIGTERM, signal.SIGINT):
                handlers[number] = signal.getsignal(number)
                signal.signal(number, signal.SIG_IGN)
        try:
            if child is not None and (child.poll() is None or active_owned_group(child.pid)): joined = stop_owned_group(child, grace)
            if child is not None: receipt['exit'] = child.returncode
            try: receipt['source_after'] = source_identity()
            except BaseException as error: receipt['source_after_error'] = type(error).__name__
            try:
                guard('final')
                receipt['final_guard_pass'] = True
            except BaseException as error: receipt['final_guard_error'] = type(error).__name__
            receipt.update(failure_category=failure, elapsed_seconds=time.time() - started, owned_group_joined=joined, source_stable=receipt['source_before'] is not None and receipt['source_before'] == receipt['source_after'], log_sha256=hashlib.sha256(log.read_bytes()).hexdigest() if log.exists() else None, outer_timeout_seconds=timeout, termination_grace_seconds=grace, kill_grace_seconds=grace)
        finally:
            for number, handler in handlers.items(): signal.signal(number, handler)
    return receipt

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('major', type=int, choices=(17, 18))
    parser.add_argument('source')
    parser.add_argument('output', type=Path)
    parser.add_argument('schema', type=int, choices=(75, 77))
    args = parser.parse_args()
    assert re.fullmatch('[a-f0-9]{40}', args.source)
    assert args.output.resolve() != ROOT and ROOT not in args.output.resolve().parents
    args.output.mkdir(mode=0o700, parents=True, exist_ok=True)
    prefix = 'postgres' + str(args.major)
    assert not any((args.output / (prefix + suffix)).exists() for suffix in ('-test.jsonl', '-receipt.json', '-capacity.jsonl', '-fences.json'))
    env = dict(os.environ, GOMAXPROCS='4', GOFLAGS='-p=2', GOWORK='off', GOTOOLCHAIN='go1.27.2')
    def guard(stage):
        result = subprocess.run(['bash', 'scripts/check-build-capacity.sh', '--json'], cwd=ROOT, capture_output=True, text=True, timeout=15)
        with (args.output / (prefix + '-capacity.jsonl')).open('a') as dest:
            dest.write(json.dumps({'stage': stage, 'time': time.time(), 'exit': result.returncode, 'stdout': result.stdout, 'stderr': result.stderr}) + '\n')
        assert result.returncode == 0, 'fatal capacity guard'
    observed = {}
    def ownership():
        assert subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, timeout=15) == b''
        assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, timeout=15).decode().strip() == args.source
        assert expected_schema_version(ROOT) == args.schema
        version = subprocess.check_output(['/usr/local/go/bin/go', 'version'], cwd=ROOT, env=env, timeout=15).decode().strip()
        assert version == 'go version go1.27.2 linux/amd64'
        url, safe = live_fence(args.major, args.schema)
        safe['private_reference'] = private_write(args.source, args.major, url)
        safe['go_version'] = version
        observed.update(safe)
        (args.output / (prefix + '-fences.json')).write_text(json.dumps(safe, indent=2) + '\n')
        env['PAGINATION_TEST_DATABASE_URL'] = url
    previous = signal.getsignal(signal.SIGTERM)
    def interrupt(_signal, _frame): raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, interrupt)
    try:
        receipt = run_lane(ARGV, env, args.output / (prefix + '-test.jsonl'), guard, ownership, lambda: fingerprint(ROOT))
    finally: signal.signal(signal.SIGTERM, previous)
    receipt.update(source=args.source, major=args.major, schema=args.schema, go_version=observed.get('go_version'))
    (args.output / (prefix + '-receipt.json')).write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps(receipt), flush=True)
    return receipt['exit'] if receipt['failure_category'] is None and receipt['owned_group_joined'] and receipt['source_stable'] and receipt['final_guard_pass'] and receipt['exit'] is not None else 1

if __name__ == '__main__':
    os.umask(0o077)
    sys.exit(main())
