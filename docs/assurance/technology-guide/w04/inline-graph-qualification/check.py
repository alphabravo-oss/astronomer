#!/usr/bin/env python3
"""Check retained graph evidence; never award whole supplier or release status."""
import copy
import gzip
import hashlib
import io
import json
from pathlib import Path
import re
import subprocess
import sys

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[4]
COSIGN_SHA = '4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71'
CLAIMS = ('authoritativeQualification', 'supplierWholeTransfer', 'upstreamSignature',
          'productionOIDCPublication', 'measuredDBFreshness', 'scanUsedWitness', 'durableProjection')
STEPS = ['selected-go-version', 'selected-node-version', 'unchanged-production-baseline',
         'focused-graph-race', 'full-graph-coupled-race', 'vet', 'json-governance',
         'complexity', 'boundaries', 'docs', 'cli-docs', 'release-contract']
FIXTURES = {
    'TestSelectedCosignProducerToVerifiedLoader',
    'TestSelectedCosignDatabaseUpdateToCanonicalLoader',
    'TestActualPublisherCopiesSignsVerifiesAndRetriesImmutableChildren',
    'TestActualPublisherClosesScannerDatabaseSubjectsAndOfflineGraph',
    'TestActualPublisherVerifiesDescriptorDatabasePayloadReadback',
    'TestActualPublisherInlineDatabaseConfigPreservesSignedOfflineGraph',
}
BAD_LEAVES = {
    'TestOfflineStoreOwnsBytesAndCannotDelegateNetwork/config-url',
    'TestOfflineStoreOwnsBytesAndCannotDelegateNetwork/config-inline',
}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def raw(name):
    path = HERE/name
    data = path.read_bytes()
    if path.suffix == '.gz':
        with gzip.GzipFile(fileobj=io.BytesIO(data)) as stream:
            data = stream.read((4 << 20) + 1)
        assert len(data) <= 4 << 20, name
    return data


def terminals(data):
    rows = []
    for line in data.decode().splitlines():
        try:
            row = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(row, dict) and row.get('Test') and row.get('Action') in ('pass', 'fail', 'skip'):
            rows.append({'package': row['Package'], 'test': row['Test'], 'action': row['Action']})
    return rows


def git(*args):
    return subprocess.check_output(['git', '-C', str(ROOT), *args], timeout=15)


def snapshot_binding(snapshot, overrides=None):
    assert snapshot['tree'] == git('rev-parse', snapshot['commit']+'^{tree}').decode().strip()
    assert len(snapshot['files']) == 137
    names = git('ls-tree', '-r', '--name-only', snapshot['commit']).decode().splitlines()
    prefixes = ('internal/delivery/resolver/', 'internal/delivery/distribution/',
                'deploy/bundles/cmd/distribution/', 'internal/httpclient/',
                'internal/jsoncontract/', 'internal/releasecontract/')
    roots = ('go.mod', 'go.sum', 'docs/data-governance/json-contracts.json',
             'frontend/package.json', 'frontend/package-lock.json')
    assert set(snapshot['files']) == {name for name in names if name in roots or name.startswith(prefixes) and name.endswith('.go')}
    for name, fingerprint in snapshot['files'].items():
        commit = (overrides or {}).get(name, snapshot['commit'])
        assert sha(git('show', commit+':'+name)) == fingerprint, name


def validate(receipt, corrected):
    assert receipt['schemaVersion'] == 1 and receipt['workPackage'] == 'W04'
    assert receipt['slice'] == 'exact-inline-supplier-configuration-graph'
    assert receipt['wholePackageStatus'] == 'IN_PROGRESS'
    assert all(receipt[key] is False for key in CLAIMS)
    assert receipt['cosignSHA256'] == COSIGN_SHA
    assert receipt['actualGoVersion'] == 'go version go1.27.2 linux/amd64'
    assert receipt['actualNodeVersion'] == 'v26.11.1'
    assert receipt['environment'] == {
        'GOTOOLCHAIN': 'go1.27.2', 'GOMAXPROCS': '4', 'GOFLAGS': '-p=2', 'GOWORK': 'off',
        'COSIGN_BIN': '/root/.cache/astronomer-032/toolchains/bin/cosign-3.1.3',
        'NODE_PATH': '/root/astronomer-all/astronomer-032-w07/frontend/node_modules',
    }
    assert receipt['overallDeadlineSeconds'] == 1800
    assert receipt['sourceUnchanged'] is True and receipt['baselineRestored'] is True
    assert receipt['frozenSource'] == receipt['sourceAfter']
    assert receipt['frozenSource']['status'] == ''
    assert receipt['frozenSource']['commit'].startswith('21e1bb3e' if corrected else '36138b21')
    assert receipt['scopedStatus'] == ('PASS' if corrected else 'FAIL')
    assert [gate['gate'] for gate in receipt['gates']] == (STEPS if corrected else STEPS[:5])
    if corrected:
        assert 'failure' not in receipt and 'finalGuardFailure' not in receipt
    else:
        assert receipt['failure'] == {'type': 'RuntimeError', 'message': 'gate failed: full-graph-coupled-race'}
    for entry in receipt['gates'] + receipt['guards']:
        assert entry['launched'] is True and entry['drained'] is True
        assert entry['leaderHeldUntilDrain'] is True
        assert entry['liveDescendantsAfterDrain'] == [] and entry['cleanupActions'] == []
        assert entry['reason'] == '' and entry['guardException'] is None
    for gate in receipt['gates']:
        assert gate['sourceBefore'] == gate['sourceAfter']
        assert gate['sourceBefore']['commit'] == receipt['frozenSource']['commit']
        baseline = gate['gate'] == 'unchanged-production-baseline'
        assert gate['expectedFailure'] is baseline
        assert gate['exitCode'] == (1 if baseline or (not corrected and gate['gate'] == STEPS[4]) else 0)
    for offset, count, parents, failed in ((2, 9, 4, 8), (3, 91, 7, 0), (4, 604, 123, 0 if corrected else 3)):
        gate = receipt['gates'][offset]
        assert (gate['cases'], gate['parents'], gate['failures'], gate['skips']) == (count, parents, failed, 0)
    assert len(receipt['guards']) == (55 if corrected else 26)
    assert all(guard['exitCode'] == 0 for guard in receipt['guards'])


def verify_packet(label, receipt):
    snapshot_binding(receipt['frozenSource'])
    for gate in receipt['gates']:
        baseline = gate['gate'] == 'unchanged-production-baseline'
        overrides = {name: receipt['baselineProductionCommit'] for name in receipt['baselineProductionPaths']} if baseline else None
        snapshot_binding(gate['sourceBefore'], overrides)
        data = raw(label+'/'+gate['gate']+'.log.gz')
        assert sha(data) == gate['sha256']
        if '-json' not in gate['command']:
            continue
        rows = terminals(data)
        assert rows == gate['terminalTests']
        assert len(rows) == gate['cases']
        assert sum('/' not in row['test'] for row in rows) == gate['parents']
        assert sum(row['action'] == 'fail' for row in rows) == gate['failures']
        assert sum(row['action'] == 'skip' for row in rows) == gate['skips']
        if baseline:
            assert {row['test'] for row in rows if row['action'] == 'fail' and '/' in row['test']} == {
                'TestDescriptorContractAuthenticatesRuntimeChildrenWithoutPayloadQualification/inline-config',
                'TestInlineDatabaseConfigRequiresExactSeparateOfflineBlob/valid',
                'TestInlineDescriptorProducerRetainsExactOriginalAndRequiresConfigMember/valid',
                'TestOfflineCaptureRetainsInlineEmptyConfigWithoutReplacementFetch/valid',
            }
        if gate['gate'] == STEPS[4]:
            assert FIXTURES <= {row['test'] for row in rows if row['action'] == 'pass'}
            if label == 'failed':
                assert {row['test'] for row in rows if row['action'] == 'fail' and '/' in row['test']} == BAD_LEAVES
    for guard in receipt['guards']:
        data = raw(label+'/'+guard['gate']+'.log.gz')
        assert sha(data) == guard['sha256']
        match = re.fullmatch(rb'check-build-capacity: path=\. mount=/ available=(\d+)GiB free=(\d+)% required=20GiB/20%\n', data)
        assert match and int(match[1]) >= 20 and int(match[2]) >= 20
    for name in receipt['baselineProductionPaths']:
        assert sha(raw(label+'/baseline-originals/'+name)) == receipt['frozenSource']['files'][name]


def main():
    index = json.loads((HERE/'bindings.json').read_bytes())
    assert index['schemaVersion'] == 1 and index['kind'] == 'InlineDatabaseGraphEvidenceBindings'
    assert len(index['files']) == 115
    for name, entry in index['files'].items():
        relative = Path(name)
        assert not relative.is_absolute() and '..' not in relative.parts
        path = HERE/relative
        assert not path.is_symlink() and all(not parent.is_symlink() for parent in path.parents if parent != HERE.parent)
        data = path.read_bytes()
        assert entry['size'] == len(data) and entry['sha256'] == sha(data), name
        if name.endswith('.gz'):
            payload = raw(name)
            assert entry['rawSize'] == len(payload) and entry['rawSHA256'] == sha(payload)
    receipts = {label: json.loads(raw(label+'/receipt.json.gz')) for label in ('failed', 'corrected')}
    for label, receipt in receipts.items():
        validate(receipt, label == 'corrected')
        verify_packet(label, receipt)
    assert sha(raw('owned_runner.py.txt')) == '60c3790c8931a5d9986979f9fec5416b1f5e1f697b2c115272db8b40acd8000d'
    assert sha(raw('failed-driver.py.txt')) == '414cb66eb6dea7d0df3801dd889cc0df0993d225a4644e3f0607423f1682793d'
    assert sha(raw('corrected-driver.py.txt')) == 'f9aebb5ce388844e998cba3cae4c90f85f6dbdb69b4f6d4e361eb07938680a14'
    assert b'Ran 7 tests' in raw('owned-runner-controls.log') and raw('owned-runner-controls.log').rstrip().endswith(b'OK')
    if '--self-test' in sys.argv:
        count = 0
        def reject(receipt, binding=False):
            nonlocal count
            try:
                if binding:
                    snapshot_binding(receipt['frozenSource'])
                else:
                    validate(receipt, True)
            except (AssertionError, KeyError, subprocess.CalledProcessError):
                count += 1
                return
            raise AssertionError('invalid evidence accepted')
        for key in CLAIMS:
            changed = copy.deepcopy(receipts['corrected']); changed[key] = True; reject(changed)
        for mutate in (
            lambda r: r.update(sourceUnchanged=False),
            lambda r: r.update(cosignSHA256='0'*64),
            lambda r: r.update(actualGoVersion='go version go1.26.6 linux/amd64'),
            lambda r: r['gates'][4].update(skips=1),
            lambda r: r['gates'][4].update(cases=603),
            lambda r: r['gates'][4].update(exitCode=1),
            lambda r: r['guards'][0].update(exitCode=1),
            lambda r: r['gates'][4].update(drained=False),
            lambda r: r['gates'][4].update(leaderHeldUntilDrain=False),
            lambda r: r.update(baselineRestored=False),
            lambda r: r['gates'][4].update(sourceAfter={}),
        ):
            changed = copy.deepcopy(receipts['corrected'])
            mutate(changed)
            reject(changed)
        changed = copy.deepcopy(receipts['corrected']); changed['frozenSource']['tree'] = '0'*40; reject(changed, True)
        changed = copy.deepcopy(receipts['corrected']); changed['frozenSource']['files'][next(iter(changed['frozenSource']['files']))] = '0'*64; reject(changed, True)
        print(f'PASS: two retained packets; {count} negative assertions; 137 Git source bindings each')
    else:
        print('PASS: failed and corrected graph evidence; supplier, freshness, witness and release remain unqualified')


if __name__ == '__main__':
    main()
