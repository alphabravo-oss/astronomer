"""Source-review draft. No mutation without the parent's explicit runtime lease."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time
from urllib.parse import parse_qsl, unquote, urlsplit, urlunsplit

DIRECTORY = Path(__file__).resolve().parent
ROOT = Path('/root/astronomer-all/astronomer-032-w02-current')
SOURCE = 'ee2c52d872608c48d775ebcc76d547634753396e'

def load(name, expected):
    path = DIRECTORY / (name + '.py')
    assert hashlib.sha256(path.read_bytes()).hexdigest() == expected
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

owned = load('owned_runner', '60c3790c8931a5d9986979f9fec5416b1f5e1f697b2c115272db8b40acd8000d')
fences = load('fences', '03f0c46e08eb01a539c12f8185700a04ac2bdb291e5bd1471d9d535b947f9b42')

def run_step(command, display, env, log, guard, ownership, identity, timeout=600):
    receipt = {'argv': display, 'source_before': None, 'source_after': None,
               'final_guard_pass': False, 'outcome': None}
    started = time.monotonic()
    try:
        guard('preflight')
        receipt['source_before'] = identity()
        ownership()
        receipt['outcome'] = owned.run_owned(command, log, ROOT, env, guard, timeout,
                                           grace=5, kill_grace=5)
    except BaseException as error:
        receipt['exception_category'] = type(error).__name__
    finally:
        # Cleanup in the exact imported helper holds the unreaped leader until
        # its live descendants are drained. Never poll/wait the leader here.
        previous = {}
        for number in (signal.SIGINT, signal.SIGTERM):
            previous[number] = signal.getsignal(number)
            signal.signal(number, signal.SIG_IGN)
        try:
            try:
                receipt['source_after'] = identity()
            except BaseException as error:
                receipt['source_after_error'] = type(error).__name__
            try:
                guard('final')
                receipt['final_guard_pass'] = True
            except BaseException as error:
                receipt['final_guard_error'] = type(error).__name__
            receipt['source_stable'] = receipt['source_before'] is not None and receipt['source_before'] == receipt['source_after']
            receipt['elapsed_seconds'] = time.monotonic() - started
            receipt['log_sha256'] = hashlib.sha256(log.read_bytes()).hexdigest() if log.exists() else None
        finally:
            for number, handler in previous.items():
                signal.signal(number, handler)
    return receipt

def passed(receipt):
    result = receipt['outcome'] or {}
    return (result.get('exitCode') == 0 and result.get('reason') == '' and result.get('drained') is True
            and receipt['final_guard_pass'] and receipt['source_stable'] and 'exception_category' not in receipt)

def psql_env(database_url):
    address = urlsplit(database_url)
    env = {key: value for key, value in os.environ.items() if not key.startswith('PG')}
    env.update(PGHOST=address.hostname, PGPORT=str(address.port), PGDATABASE=address.path.lstrip('/'),
               PGUSER=unquote(address.username), PGPASSWORD=unquote(address.password), PGCONNECT_TIMEOUT='5',
               PGOPTIONS='-c statement_timeout=15000 -c lock_timeout=5000')
    for key, value in parse_qsl(address.query, keep_blank_values=True, strict_parsing=True):
        if key == 'sslmode': env['PGSSLMODE'] = value
        if key == 'sslrootcert': env['PGSSLROOTCERT'] = value
    return env

INDEX_SQL = """SELECT c.relname, i.indisvalid, i.indisready, pg_get_indexdef(i.indexrelid), pg_relation_size(i.indexrelid)
FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='public' AND c.relname IN
('idx_control_plane_alerts_controller_keyset','idx_control_plane_alerts_controller_status_keyset')
ORDER BY c.relname"""
INDEXES = {
    'idx_control_plane_alerts_controller_keyset': 'CREATE INDEX idx_control_plane_alerts_controller_keyset ON public.control_plane_alerts USING btree (controller, fired_at DESC, id DESC)',
    'idx_control_plane_alerts_controller_status_keyset': 'CREATE INDEX idx_control_plane_alerts_controller_status_keyset ON public.control_plane_alerts USING btree (controller, status, fired_at DESC, id DESC)',
}

def credential_free_url(database_url):
    address = urlsplit(database_url)
    host = '[' + address.hostname + ']' if ':' in address.hostname else address.hostname
    return urlunsplit(address._replace(netloc=host + ':' + str(address.port)))

def inspect_indexes(database_url, version):
    result = subprocess.run(['psql', '-X', '-At', '-v', 'ON_ERROR_STOP=1', '-c', INDEX_SQL],
                            env=psql_env(database_url), capture_output=True, text=True, timeout=20)
    assert result.returncode == 0, 'index inspection failed; details suppressed'
    rows = [line.split('|') for line in result.stdout.splitlines()]
    expected = list(INDEXES)[:version - 75]
    assert [row[0] for row in rows] == expected, 'unexpected owned index inventory'
    for name, valid, ready, definition, size in rows:
        assert valid == ready == 't' and definition == INDEXES[name] and int(size) > 0
    return [{'name': row[0], 'valid': True, 'ready': True, 'definition': row[3], 'bytes': int(row[4])} for row in rows]

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('major', type=int, choices=(17, 18))
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    os.umask(0o077)
    args.output.mkdir(mode=0o700, parents=True, exist_ok=False)
    assert ROOT not in args.output.resolve().parents and args.output.resolve() != ROOT
    env = dict(os.environ, GOTOOLCHAIN='go1.27.2', GOWORK='off', GOMAXPROCS='4', GOFLAGS='-p=2')
    expected_version = 75
    database_url = None
    steps = []
    observations = []
    receipt = {'source': SOURCE, 'major': args.major, 'steps': steps, 'observations': observations,
               'interrupted_invalid_index_recovery': 'NOT_RUN_SEPARATE_SOURCE_REVIEW_REQUIRED'}
    def guard(stage):
        result = subprocess.run(['bash', 'scripts/check-build-capacity.sh', '--json'], cwd=ROOT,
                                capture_output=True, text=True, timeout=15)
        with (args.output / 'capacity.jsonl').open('a') as stream:
            stream.write(json.dumps({'stage': stage, 'time': time.time(), 'exit': result.returncode,
                                     'stdout': result.stdout, 'stderr': result.stderr}) + '\n')
        assert result.returncode == 0, 'fatal capacity guard'
    def ownership():
        nonlocal database_url
        assert subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, timeout=15) == b''
        assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, timeout=15).decode().strip() == SOURCE
        assert fences.expected_schema_version(ROOT) == 77
        assert subprocess.check_output(['/usr/local/go/bin/go', 'version'], cwd=ROOT, env=env, timeout=15).decode().strip() == 'go version go1.27.2 linux/amd64'
        url, observation = fences.live_fence(args.major, expected_version, database_url)
        if database_url is None:
            observation['private_reference'] = fences.private_write(SOURCE, args.major, url)
        database_url = url
        observations.append(observation)
    def step(name, command, display=None):
        result = run_step(command, display or command, env, args.output / (name + '.log'), guard,
                          ownership, lambda: fences.fingerprint(ROOT))
        steps.append({'name': name, **result})
        assert passed(result), 'bounded step failed; category retained in receipt'
    def interrupt(_number, _frame):
        raise RuntimeError('owned runner interrupted')
    previous = {number: signal.signal(number, interrupt) for number in (signal.SIGINT, signal.SIGTERM)}
    try:
        guard('initial')
        receipt['source_before'] = fences.fingerprint(ROOT)
        ownership()
        receipt['before_indexes'] = inspect_indexes(database_url, 75)
        step('go-version', ['/usr/local/go/bin/go', 'version'])
        assert (args.output / 'go-version.log').read_text().strip() == 'go version go1.27.2 linux/amd64'
        receipt['go_version'] = 'go version go1.27.2 linux/amd64'
        binary = args.output / 'migrator'
        step('build', ['/usr/local/go/bin/go', 'build', '-trimpath', '-o', str(binary), './cmd/migrator'])
        receipt['migrator_sha256'] = hashlib.sha256(binary.read_bytes()).hexdigest()
        # Only the exact additive 75..77 edges are in this qualification scope.
        for ordinal, version in enumerate((77, 76, 75, 77), start=1):
            label = f'{ordinal}-migrate-{expected_version}-to-{version}'
            # lib/pq accepts private credentials through PGUSER/PGPASSWORD;
            # never place the password or userinfo in process argv.
            env.update({key: value for key, value in psql_env(database_url).items() if key.startswith('PG')})
            command = [str(binary), '-database', credential_free_url(database_url), '-path', 'internal/db/migrations', 'goto', str(version)]
            step(label, command, [str(binary), '-database', '[PRIVATE_REFERENCE]', '-path', 'internal/db/migrations', 'goto', str(version)])
            expected_version = version
            ownership()
            receipt[label + '-indexes'] = inspect_indexes(database_url, version)
        receipt['migration_edges_pass'] = True
    except BaseException as error:
        receipt['failure_category'] = type(error).__name__
    finally:
        try:
            receipt['source_after'] = fences.fingerprint(ROOT)
            receipt['source_stable'] = receipt.get('source_before') == receipt['source_after']
        except BaseException as error:
            receipt['source_after_error'] = type(error).__name__
            receipt['source_stable'] = False
        try:
            guard('runner-final')
            receipt['runner_final_guard_pass'] = True
        except BaseException as error:
            receipt['runner_final_guard_pass'] = False
            receipt['runner_final_guard_error'] = type(error).__name__
        (args.output / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
        for number, handler in previous.items(): signal.signal(number, handler)
    print(json.dumps({'receipt': str(args.output / 'receipt.json'), 'migration_edges_pass': receipt.get('migration_edges_pass', False),
                      'failure_category': receipt.get('failure_category')}))
    return 0 if receipt.get('migration_edges_pass') and receipt['runner_final_guard_pass'] and receipt['source_stable'] else 1

if __name__ == '__main__':
    sys.exit(main())
