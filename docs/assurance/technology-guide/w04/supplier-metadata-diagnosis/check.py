#!/usr/bin/env python3
"""Check retained failed-capture evidence; never grant release qualification."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[4]

def sha(data):
    return hashlib.sha256(data).hexdigest()

def validate(receipt, observation, manifests):
    assert receipt['schemaVersion'] == 1
    assert receipt['kind'] == 'SupplierMetadataLocalEvidence'
    for key in ('authoritativeReleaseQualification', 'productionOIDCPublication',
                'durableProjection', 'measuredScanUsedDB'):
        assert receipt[key] is False
    assert receipt['retainedPayloads'] == []
    assert receipt['sourceBeforeAfterByteEqual'] is True
    assert receipt['sourceBefore'] == receipt['sourceAfter']
    assert receipt['environment'] == dict(GOTOOLCHAIN='go1.27.2', GOMAXPROCS='4',
                                          GOFLAGS='-p=2', GOWORK='off')
    assert [step['exitCode'] for step in receipt['steps']] == [0, 0, 0, 1, 0]
    assert observation == receipt['observation']
    assert observation['release_qualification'] is False
    assert observation['expanded_byte_count_measured'] is False
    assert observation['canonical_maximum_expanded_bytes'] == 8 << 30
    roles = observation['results']
    assert [r['role'] for r in roles] == ['trivy-database', 'trivy-java-database']
    assert len(receipt['retainedMetadata']) == 2
    for result, retained in zip(roles, receipt['retainedMetadata']):
        assert result['status'] == 'capture-failed'
        assert result['metadata_inspected'] is False and result['fresh_at_completion'] is False
        exchanges = result['exchanges']
        assert [(e['method'], e['kind'], e['status']) for e in exchanges] == [
            ('HEAD', 'metadata', 401), ('GET', 'token', 200),
            ('HEAD', 'metadata', 200), ('GET', 'metadata', 200)]
        assert all(e['origin'] == 'https://ghcr.io' for e in exchanges)
        response = exchanges[-1]
        raw = manifests[retained['name']]
        assert 0 < len(raw) <= 1 << 20
        assert retained['size'] == response['bytes_read'] == len(raw)
        assert retained['sha256'] == response['original_manifest_sha256'] == sha(raw)
        assert response['subject_digest'] == exchanges[-2]['subject_digest'] == 'sha256:' + sha(raw)
        document = json.loads(raw)
        assert document['schemaVersion'] == 2 and len(document['layers']) == 1
        config = document['config']
        assert config['mediaType'] == 'application/vnd.oci.empty.v1+json'
        assert config['data'] == 'e30=' and config['size'] == 2
        assert config['digest'] == 'sha256:' + sha(b'{}')
    return True

def main():
    index = json.loads((HERE / 'bindings.json').read_bytes())
    assert index['schemaVersion'] == 1 and index['kind'] == 'SupplierMetadataDiagnosisBindings'
    for name, binding in index['files'].items():
        assert Path(name).name == name
        path = HERE / name
        assert not path.is_symlink() and path.is_file()
        data = path.read_bytes()
        assert binding == {'sha256': sha(data), 'size': len(data)}, name
    receipt = json.loads((HERE / 'receipt.json').read_bytes())
    observation = json.loads((HERE / 'transfer.log').read_bytes())
    manifests = {r['name']: (HERE / r['name']).read_bytes() for r in receipt['retainedMetadata']}
    assert sha((HERE / 'runner.go.txt').read_bytes()) == receipt['runnerSHA256']
    assert sha((HERE / 'driver.py.txt').read_bytes()) == receipt['driverSHA256']
    snapshot = receipt['sourceBefore']
    tree = subprocess.check_output(['git', '-C', str(ROOT), 'rev-parse', snapshot['commit'] + '^{tree}'], text=True).strip()
    assert tree == snapshot['tree']
    assert len(snapshot['runtimeSourceSHA256']) == 50
    for name, fingerprint in snapshot['runtimeSourceSHA256'].items():
        assert not Path(name).is_absolute() and '..' not in Path(name).parts
        data = subprocess.check_output(['git', '-C', str(ROOT), 'show', snapshot['commit'] + ':' + name])
        assert sha(data) == fingerprint, name
    for step in receipt['steps']:
        assert sha((HERE / step['log']).read_bytes()) == step['sha256']
    validate(receipt, observation, manifests)
    if '--self-test' in sys.argv:
        def reject(changed, observed=observation, raw=manifests):
            try:
                validate(changed, observed, raw)
            except (AssertionError, KeyError):
                return
            raise AssertionError('invalid evidence accepted')
        count = 0
        for key in ('authoritativeReleaseQualification', 'productionOIDCPublication', 'durableProjection', 'measuredScanUsedDB'):
            changed = copy.deepcopy(receipt); changed[key] = True; reject(changed); count += 1
        changed = copy.deepcopy(receipt); changed['sourceAfter']['tree'] = '0' * 40; reject(changed); count += 1
        changed = copy.deepcopy(receipt); changed['steps'][3]['exitCode'] = 0; reject(changed); count += 1
        changed = copy.deepcopy(receipt); changed['retainedMetadata'][0]['sha256'] = '0' * 64; reject(changed); count += 1
        changed = copy.deepcopy(receipt); changed['retainedPayloads'] = ['invented']; reject(changed); count += 1
        changed = copy.deepcopy(receipt); changed['environment']['GOTOOLCHAIN'] = 'go1.26.6'; reject(changed); count += 1
        changed_raw = dict(manifests); name = receipt['retainedMetadata'][0]['name']; changed_raw[name] = b'{}'; reject(receipt, raw=changed_raw); count += 1
        print(f'PASS: one positive, {count} negative failed-capture consistency assertions; 50 Git source bindings')
    else:
        print('PASS: retained failed-capture evidence consistency; no qualification granted')

if __name__ == '__main__':
    main()
