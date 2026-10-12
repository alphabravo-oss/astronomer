#!/usr/bin/env python3
"""Proposed strict collector. No scan is qualified by a supplied JSON assertion."""
import argparse
import hashlib
import importlib.util
import json
import os
import re
from pathlib import Path
import signal
import subprocess
import time

MAX_REPORT = 64 << 20
MAX_LOG = 8 << 20
MODULE = 'github.com/zricethezav/gitleaks/v8 v8.30.1'
GO_VERSION = 'go version go1.27.2 linux/amd64'
OWNED_SHA = '60c3790c8931a5d9986979f9fec5416b1f5e1f697b2c115272db8b40acd8000d'


def digest(path):
    value = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(1 << 20), b''):
            value.update(block)
    return value.hexdigest()


def identity(path):
    path = Path(path)
    assert path.is_file() and not path.is_symlink(), 'executable/config identity unavailable'
    item = path.stat()
    return dict(path=str(path), sha256=digest(path), device=item.st_dev,
                inode=item.st_ino, size=item.st_size, mtime_ns=item.st_mtime_ns)


def write_new(path, row):
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), 'w') as stream:
        json.dump(row, stream, indent=2)
        stream.write('\n')


def load_owned():
    path = Path(__file__).with_name('owned_process.py')
    assert digest(path) == OWNED_SHA, 'owned process helper drift'
    spec = importlib.util.spec_from_file_location('history_owned', path)
    owned = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(owned)
    return owned


def validate_report(raw, exit_code):
    assert len(raw) <= MAX_REPORT, 'report budget exceeded'
    def closed_pairs(pairs):
        row = {}
        for key, value in pairs:
            assert key not in row, 'duplicate report key'
            row[key] = value
        return row
    report = json.loads(raw, object_pairs_hook=closed_pairs)
    assert isinstance(report, list) and bool(report) == (exit_code == 42), 'report/status mismatch'
    keys = ('Commit', 'File', 'RuleID', 'Fingerprint', 'StartLine', 'EndLine', 'StartColumn', 'EndColumn')
    seen = set()
    for row in report:
        assert isinstance(row, dict) and all(key in row for key in keys), 'report site missing'
        assert row.get('Secret') == 'REDACTED' and isinstance(row.get('Match'), str) and 'REDACTED' in row['Match'], 'unredacted report'
        key = tuple(row[name] for name in keys)
        assert isinstance(row['Commit'],str) and re.fullmatch(r'[0-9a-f]{40}',row['Commit']), 'invalid finding commit'
        assert all(isinstance(row[name],str) and row[name] for name in keys[:4]), 'invalid finding identity'
        assert all(type(row[name]) is int and row[name]>0 for name in keys[4:]), 'invalid finding coordinates'
        assert row['StartLine']<=row['EndLine'], 'invalid finding span'
        finding_path=Path(row['File'])
        assert not finding_path.is_absolute() and '..' not in finding_path.parts and ':' not in row['File'] and chr(92) not in row['File'], 'invalid finding path'
        assert key not in seen, 'duplicate report site'
        seen.add(key)
    return len(report)


def collect(repo, scanner, go, go_sha, config, manifest, output, timeout=1800, guard=None):
    repo, scanner, go, config, output = (Path(value).absolute() for value in (repo, scanner, go, config, output))
    assert 0 < timeout <= 3600, 'invalid collector deadline'
    assert repo.resolve() == repo and output.resolve().parent == output.parent, 'symlinked collection root'
    assert not output.is_relative_to(repo) and output.parent.is_dir(), 'output must be outside source in an existing parent'
    os.umask(0o077)
    output.mkdir(mode=0o700, exist_ok=False)
    owned = None
    report = output / 'gitleaks.json'
    receipt = {'schema_version': 1, 'steps': [], 'collection_complete': False,
               'scope': 'Fresh full-history collection only; no finding or release disposition'}
    deadline = time.monotonic() + timeout
    before, refs_before, after, refs_after = None, None, None, None
    heads = {}
    scanner_before, go_before, config_before = None, None, None
    env = dict(os.environ, GOTOOLCHAIN='local', GOWORK='off', GOMAXPROCS='4')
    for key in list(env):
        if key.startswith('GITLEAKS_'):
            del env[key]
    env['PATH'] = str(go.parent) + ':' + env['PATH']
    originals = {}

    def capacity(stage):
        if guard is not None:
            guard(stage)
        else:
            result = subprocess.run(['bash', 'scripts/check-build-capacity.sh', '--json'], cwd=repo,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=15)
            with (output / 'capacity.jsonl').open('ab') as stream:
                stream.write(json.dumps({'stage': stage, 'exit': result.returncode,
                                         'stdout':result.stdout.decode(errors='replace')[:16384],
                                         'stderr':result.stderr.decode(errors='replace')[:16384]}).encode() + b'\n')
            assert result.returncode == 0, 'fatal capacity guard'
        if scanner_before is not None:
            assert identity(scanner) == scanner_before and identity(go) == go_before and identity(config) == config_before, 'tool/config changed'
        assert not report.exists() or report.stat().st_size <= MAX_REPORT, 'report budget exceeded'
        assert all(path.stat().st_size <= MAX_LOG for path in output.glob('*.log')), 'log budget exceeded'

    def interrupt(_number, _frame):
        for number in (signal.SIGINT, signal.SIGTERM):
            signal.signal(number, signal.SIG_IGN)
        raise RuntimeError('owned collector interrupted')

    def step(name, command, cap=30, accepted=(0,)):
        capacity('pre-' + name)
        remaining = deadline - time.monotonic()
        assert remaining > 0, 'collector overall deadline'
        result = owned.run_owned(command, output / (name + '.log'), repo, env, capacity,
                                 min(cap, remaining), interval=1, grace=5, kill_grace=5)
        # Error payloads and scanner output stay in private files, never stdout.
        result['guardException'] = {'type': result['guardException']['type']} if result.get('guardException') else None
        receipt['steps'].append({'name': name, 'argv': command, 'outcome': result,
                                 'log_sha256': digest(output / (name + '.log'))})
        capacity('post-' + name)
        assert result['drained'] and not result['reason'] and result['exitCode'] in accepted, 'collector child failed'
        return result['exitCode']

    def snapshot(name):
        step(name + '-status', ['git', 'status', '--porcelain=v1', '--untracked-files=all'])
        assert (output / (name + '-status.log')).read_bytes()==b'', 'dirty or untracked current source'
        step(name + '-head', ['git', 'rev-parse', '--verify', 'HEAD'])
        head=(output / (name + '-head.log')).read_text().strip()
        assert re.fullmatch(r'[0-9a-f]{40}',head), 'invalid committed HEAD'
        heads[name]=head
        step(name + '-source', ['python3', 'scripts/hash-source-tree.py', '--root', str(repo)])
        source = json.loads((output / (name + '-source.log')).read_bytes())
        assert source['schema_version'] == 2 and isinstance(source['source_tree_sha256'], str), 'source snapshot invalid'
        step(name + '-refs', ['git', 'show-ref', '--head'])
        refs = digest(output / (name + '-refs.log'))
        return source['source_tree_sha256'], refs

    try:
        originals = {number: signal.signal(number, interrupt) for number in (signal.SIGINT, signal.SIGTERM)}
        capacity('initial')
        owned = load_owned()
        scanner_before, go_before, config_before = identity(scanner), identity(go), identity(config)
        assert scanner_before['sha256'] == manifest['scanner']['executable_sha256'], 'scanner identity not reviewed'
        assert config_before['sha256'] == manifest['scanner']['config_sha256'], 'scanner configuration drift'
        assert go_before['sha256'] == go_sha, 'selected Go identity drift'
        before, refs_before = snapshot('before')
        step('go-version', [str(go), 'version'])
        assert (output / 'go-version.log').read_text().strip() == GO_VERSION, 'selected Go version drift'
        step('scanner-build-metadata', [str(go), 'version', '-m', str(scanner)])
        metadata = (output / 'scanner-build-metadata.log').read_text().splitlines()
        assert metadata and metadata[0].endswith(': go1.27.2'), 'scanner build toolchain drift'
        mods=[line.strip().split('\t') for line in metadata if line.strip().startswith('mod\t')]
        assert len(mods)==1 and len(mods[0])==4 and mods[0][1:3]==['github.com/zricethezav/gitleaks/v8','v8.30.1'], 'scanner module/version drift'
        status = step('gitleaks', [str(scanner), 'git', '--config=' + str(config), '--redact=100',
                      '--exit-code=42', '--report-format=json', '--report-path=' + str(report),
                      '--no-banner', '--log-level=error', '--log-opts=--all', '.'], cap=timeout, accepted=(0, 42))
        assert report.is_file() and not report.is_symlink() and report.stat().st_size<=MAX_REPORT, 'regular bounded report required'
        receipt['finding_count'] = validate_report(report.read_bytes(), status)
        receipt['exit_code'] = status
        after, refs_after = snapshot('after')
        assert before == after and refs_before == refs_after and heads.get('before')==heads.get('after'), 'source, HEAD or refs changed during history collection'
        receipt['collection_complete'] = True
    except BaseException as error:
        receipt['failure_category'] = type(error).__name__
    finally:
        for number in originals:
            signal.signal(number, signal.SIG_IGN)
        try:
            capacity('collector-final')
            receipt['final_guard_pass'] = True
        except BaseException as error:
            receipt['final_failure_category'] = type(error).__name__
            receipt['collection_complete'] = False
        receipt['scanner_before'] = scanner_before
        receipt['go_before'] = go_before
        receipt['config_before'] = config_before
        try:
            receipt['scanner_after'],receipt['go_after'],receipt['config_after']=identity(scanner),identity(go),identity(config)
        except BaseException as error:
            receipt['identity_failure_category']=type(error).__name__
            receipt['collection_complete']=False
        receipt['head_before'],receipt['head_after']=heads.get('before'),heads.get('after')
        receipt['source_before_sha256'], receipt['source_after_sha256'] = before, after
        receipt['refs_before_sha256'], receipt['refs_after_sha256'] = refs_before, refs_after
        receipt['report_sha256'] = digest(report) if report.is_file() and not report.is_symlink() else None
        collection = dict(schema_version=1, scanner_sha256=(scanner_before or {}).get('sha256'), scanner_module=MODULE,
                          config_sha256=(config_before or {}).get('sha256'), exit_code=receipt.get('exit_code'),
                          redact_percent=100, log_opts='--all', report_sha256=receipt['report_sha256'],
                          refs_before_sha256=refs_before, refs_after_sha256=refs_after,
                          source_before_sha256=before, source_after_sha256=after,
                          collection_complete=receipt['collection_complete'])
        write_new(output / 'collection.json', collection)
        write_new(output / 'collector-receipt.json', receipt)
        for number, handler in originals.items():
            signal.signal(number, handler)
    return receipt


def main():
    parser = argparse.ArgumentParser()
    for name in ('repo', 'scanner', 'go', 'config', 'manifest', 'output'):
        parser.add_argument('--' + name, type=Path, required=True)
    parser.add_argument('--go-sha256', required=True)
    parser.add_argument('--timeout', type=int, default=1800)
    args = parser.parse_args()
    manifest = json.loads(args.manifest.read_bytes())
    receipt = collect(args.repo, args.scanner, args.go, args.go_sha256, args.config, manifest, args.output, args.timeout)
    print(json.dumps({'collection_complete': receipt['collection_complete'], 'receipt': str(args.output / 'collector-receipt.json')}))
    return 0 if receipt['collection_complete'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
