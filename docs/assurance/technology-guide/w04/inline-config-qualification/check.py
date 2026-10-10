#!/usr/bin/env python3
"""Validate this resolver-only packet without awarding supplier acceptance."""
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

def terminals(path):
    rows = []
    for line in path.read_text().splitlines():
        try:
            row = json.loads(line)
        except json.JSONDecodeError:
            continue
        if row.get('Test') and row.get('Action') in ('pass', 'fail', 'skip'):
            rows.append(row)
    return rows

def validate(results, before, after, observer):
    assert before == after
    assert len(before['files']) == 92
    assert len(results) == 14 and all(r['exitCode'] == 0 for r in results)
    focused, full = results[1], results[3]
    assert focused['gate'] == 'focused-race' and full['gate'] == 'full-resolver-race'
    for result, count, parents in ((focused, 48, 3), (full, 136, 42)):
        assert (result['cases'], result['parents'], result['failures'], result['skips']) == (count, parents, 0, 0)
        assert result['producerFixturePassed'] is False
    assert observer['kind'] == 'SupplierMetadataLocalEvidence'
    for key in ('authoritativeReleaseQualification', 'durableProjection', 'productionOIDCPublication', 'measuredScanUsedDB'):
        assert observer[key] is False
    assert observer['retainedPayloads'] == []
    assert observer['sourceBeforeAfterByteEqual'] is True
    assert observer['sourceBefore'] == observer['sourceAfter']
    assert observer['sourceBefore']['commit'] == before['commit']
    assert len(observer['sourceBefore']['runtimeSourceSHA256']) == 51
    assert all(r['exitCode'] == 0 for r in observer['steps'])
    observation = observer['observation']
    assert observation['release_qualification'] is False and observation['expanded_byte_count_measured'] is False
    assert [r['role'] for r in observation['results']] == ['trivy-database', 'trivy-java-database']
    for result in observation['results']:
        assert result['status'] == 'descriptors-captured-not-payload'
        assert result['metadata_inspected'] is False and result['fresh_at_completion'] is False
        assert [(e['method'], e['kind'], e['status']) for e in result['exchanges']] == [('HEAD', 'metadata', 401), ('GET', 'token', 200), ('HEAD', 'metadata', 200), ('GET', 'metadata', 200)]
    return True

def main():
    index = json.loads((HERE/'bindings.json').read_bytes())
    assert index['schemaVersion'] == 1 and index['kind'] == 'InlineOCIConfigQualificationBindings'
    for name, binding in index['files'].items():
        path = HERE/name
        assert not Path(name).is_absolute() and '..' not in Path(name).parts
        assert path.is_file() and not path.is_symlink()
        data = path.read_bytes()
        assert binding == {'sha256': sha(data), 'size': len(data)}, name
    read = lambda name: json.loads((HERE/name).read_bytes())
    results, before, after, observer = read('corrected/results.json'), read('corrected/before.json'), read('corrected/after.json'), read('observer/receipt.json')
    validate(results, before, after, observer)
    for snapshot, key in ((before, 'files'), (observer['sourceBefore'], 'runtimeSourceSHA256')):
        tree = subprocess.check_output(['git', '-C', str(ROOT), 'rev-parse', snapshot['commit']+'^{tree}'], text=True).strip()
        assert tree == snapshot['tree']
        for name, fingerprint in snapshot[key].items():
            data = subprocess.check_output(['git', '-C', str(ROOT), 'show', snapshot['commit']+':'+name])
            assert sha(data) == fingerprint, name
    for result in results:
        path = HERE/'corrected'/(result['gate']+'.log')
        assert sha(path.read_bytes()) == result['sha256']
        if result['gate'].endswith('race'):
            ends = terminals(path)
            assert len(ends) == result['cases']
            assert sum('/' not in r['Test'] for r in ends) == result['parents']
            assert all(r['Action'] == 'pass' for r in ends)
    baseline = read('baseline/summary.json')
    assert baseline['exitCode'] == 1 and baseline['sourceUnchanged'] is True
    assert read('baseline/before.json') == read('baseline/after.json')
    expected = {'TestVerifiedArtifactMetadataAuthenticatesLargeDescriptorWithoutFetchingPayload/inline-config', 'TestSupplierMetadataPinsOneResolutionWithoutSignatureOrPayloadReads/inline-config'}
    assert {r['Test'] for r in terminals(HERE/'baseline/race.jsonl') if r['Action']=='fail' and '/' in r['Test']} == expected
    assert sha((HERE/'baseline/race.jsonl').read_bytes()) == baseline['logSHA256']
    for step in observer['steps']:
        assert sha((HERE/'observer'/step['log']).read_bytes()) == step['sha256']
    assert read('observer/transfer.log') == observer['observation']
    for retained in observer['retainedMetadata']:
        body = (HERE/'observer'/retained['name']).read_bytes()
        assert retained['size'] == len(body) and retained['sha256'] == sha(body)
    if '--self-test' in sys.argv:
        def reject(r=results, b=before, a=after, o=observer):
            try:
                validate(r,b,a,o)
            except (AssertionError, KeyError):
                return
            raise AssertionError('invalid evidence accepted')
        count = 0
        for key in ('authoritativeReleaseQualification', 'durableProjection', 'productionOIDCPublication', 'measuredScanUsedDB'):
            changed=copy.deepcopy(observer);changed[key]=True;reject(o=changed);count+=1
        changed=copy.deepcopy(after);changed['tree']='0'*40;reject(a=changed);count+=1
        changed=copy.deepcopy(results);changed[0]['exitCode']=1;reject(r=changed);count+=1
        changed=copy.deepcopy(results);changed[3]['cases']=137;reject(r=changed);count+=1
        changed=copy.deepcopy(results);changed[3]['skips']=1;reject(r=changed);count+=1
        changed=copy.deepcopy(results);changed[3]['producerFixturePassed']=True;reject(r=changed);count+=1
        changed=copy.deepcopy(observer);changed['retainedPayloads']=['invented'];reject(o=changed);count+=1
        changed=copy.deepcopy(observer);changed['observation']['results'][0]['fresh_at_completion']=True;reject(o=changed);count+=1
        print(f'PASS: one positive, {count} negative assertions; 92 gate and 51 observer Git bindings')
    else:
        print('PASS: resolver-only evidence consistency; whole supplier acceptance remains open')
if __name__=='__main__':
    main()
