#!/usr/bin/env python3
"""Bind local W04 test evidence; unsigned JSON never approves a release."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[4]
DIRECTORY = Path('docs/assurance/technology-guide/w04')
FRESHNESS = {
    'TestMeasuredFreshDatabaseAndReportCanQualifyCleanPosture',
    'TestUnknownStaleOrFailedProvenanceNeverQualifiesCleanPosture',
    'TestScanCompletionAndNextUpdateCannotRefreshOldDatabase',
    'TestExpiredReportRemainsVisibleAcrossIndependentFailures',
}
FIELDS = {'schemaVersion','workPackage','wholePackageStatus','authoritativeQualification','sourceCommit','sourceTreeGitObject','baseline','runs','environment','cosignSHA256','qualificationLimits','scope'}
LIMITS = {'supplierNetworkLayerTransfer','actualSupplierPayloadCadence','productionOIDCPublication','durableProjection','measuredScanUsedDB','APIUIFreshnessComposition','freshCacheNoPublicEgress','wholeScannerSuite','releaseApproval','combinedIntegrationQualification'}


def owned(path):
    relative = Path(path)
    if relative.is_absolute() or '..' in relative.parts or (ROOT / relative).is_symlink():
        raise ValueError('unowned evidence path')
    result = (ROOT / relative).resolve(strict=True)
    result.relative_to(ROOT)
    if not result.is_file():
        raise ValueError('nonregular evidence path')
    return result


def digest(path):
    return hashlib.sha256(owned(path).read_bytes()).hexdigest()


def load_bound(reference):
    if digest(reference['receipt']) != reference['sha256']:
        raise ValueError('changed raw receipt')
    return json.loads(owned(reference['receipt']).read_text())


def events(raw):
    race = [gate for gate in raw['gates'] if gate['name'] == 'race']
    if len(race) != 1:
        raise ValueError('race invocation missing')
    rows = []
    for line in owned(race[0]['retainedLog']).read_text().splitlines():
        try:
            rows.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return rows


def counts(rows):
    return {kind: sum(row.get('Action') == kind and bool(row.get('Test')) for row in rows) for kind in ('pass','fail','skip')}


def validate(receipt):
    if set(receipt) != FIELDS or set(receipt['qualificationLimits']) != LIMITS or set(receipt['environment']) != {'GOTOOLCHAIN','GOMAXPROCS','GOFLAGS','COSIGN_BIN'}:
        raise ValueError('open or missing evidence fields')
    if receipt['schemaVersion'] != 1 or receipt['workPackage'] != 'W04' or receipt['wholePackageStatus'] != 'IN_PROGRESS' or receipt['authoritativeQualification'] is not False or any(value is not False for value in receipt['qualificationLimits'].values()):
        raise ValueError('unearned qualification authority')
    tree = subprocess.check_output(['git','rev-parse',receipt['sourceCommit']+'^{tree}'],cwd=ROOT,text=True).strip()
    if tree != receipt['sourceTreeGitObject']:
        raise ValueError('changed source tree')
    expected_environment = {'GOTOOLCHAIN':'go1.27.2','GOMAXPROCS':'4','GOFLAGS':'-p=2','COSIGN_BIN':'/root/.cache/astronomer-032/toolchains/bin/cosign-3.1.3'}
    if receipt['environment'] != expected_environment or hashlib.sha256(Path(expected_environment['COSIGN_BIN']).read_bytes()).hexdigest() != receipt['cosignSHA256']:
        raise ValueError('changed selected tool invocation or bytes')
    baseline = load_bound(receipt['baseline'])
    if receipt['baseline']['status'] != 'FAILED_AND_PRESERVED' or baseline['sourceBefore']['commit'] != receipt['baseline']['sourceCommit'] or counts(events(baseline)) != {'pass':288,'fail':4,'skip':0} or receipt['baseline']['caseCounts'] != {'pass':288,'fail':4,'skip':0}:
        raise ValueError('failed baseline erased')
    for gate in baseline['gates']:
        if digest(gate['retainedLog']) != gate['logSHA256'] or gate['exitCode'] != (1 if gate['name'] == 'race' else 0):
            raise ValueError('changed baseline log/result')
    if len(receipt['runs']) != 2:
        raise ValueError('missing bounded run')
    expected = [('supplier-and-signed-consumer',75,294,2),('pure-freshness-evaluator',4,48,1)]
    captured_sources = None
    for reference, (kind, parents, cases, packages) in zip(receipt['runs'], expected):
        if set(reference) != {'kind','receipt','sha256','parentPasses','caseCounts','packageCount','gateCount','sourceInputCount'} or reference['kind'] != kind or reference['parentPasses'] != parents or reference['caseCounts'] != {'pass':cases,'fail':0,'skip':0} or reference['packageCount'] != packages or reference['gateCount'] != 5 or reference['sourceInputCount'] != 2576:
            raise ValueError('changed run schema or counters')
        raw = load_bound(reference)
        before, after = raw['sourceBefore'], raw['sourceAfter']
        if before != after or before['commit'] != receipt['sourceCommit'] or before['tree'] != tree or len(before['sourceSHA256']) != 2576 or raw['environment'] != expected_environment or raw['cosignSHA256'] != receipt['cosignSHA256'] or len(raw['gates']) != 5:
            raise ValueError('changed source or environment record')
        if captured_sources is None:
            for path, expected_digest in before['sourceSHA256'].items():
                if digest(path) != expected_digest:
                    raise ValueError('changed source bytes')
            captured_sources = before['sourceSHA256']
        elif before['sourceSHA256'] != captured_sources:
            raise ValueError('independent runs used different source bytes')
        if [gate['name'] for gate in raw['gates']] != ['race-capacity','race','vet-capacity','vet','capacity-final']:
            raise ValueError('changed bounded gate identities')
        for gate in raw['gates']:
            if gate['exitCode'] != 0 or digest(gate['retainedLog']) != gate['logSHA256']:
                raise ValueError('failed or changed retained gate')
        rows = events(raw)
        actual_parents = sum(row.get('Action') == 'pass' and bool(row.get('Test')) and '/' not in row['Test'] for row in rows)
        actual_packages = [row for row in rows if row.get('Action') == 'pass' and not row.get('Test')]
        if counts(rows) != reference['caseCounts'] or actual_parents != parents or len(actual_packages) != packages:
            raise ValueError('raw events disagree with counters')
        required = {'TestSelectedCosignDatabaseUpdateToCanonicalLoader','TestDatabaseUpdateConstructorRejectsUnsignedOrUnboundedIntent/mutable-feed','TestDatabasePayloadArchiveAndMetadataNegatives/oversized-member'} if packages == 2 else FRESHNESS
        for name in required:
            terminals = [row for row in rows if row.get('Test') == name and row.get('Action') in ('pass','fail','skip')]
            if len(terminals) != 1 or terminals[0]['Action'] != 'pass':
                raise ValueError('selected fixture did not execute successfully')


def main():
    receipt = json.loads((ROOT / DIRECTORY / 'signed-update-core-evidence.json').read_text())
    validate(receipt)
    fixtures = ('tree','raw-receipt','counts','tool','authority','whole-done','supplier','provenance','unknown','missing','nested-unknown')
    for name in fixtures:
        changed = copy.deepcopy(receipt)
        if name == 'tree': changed['sourceTreeGitObject'] = '0' * 40
        if name == 'raw-receipt': changed['runs'][0]['sha256'] = '0' * 64
        if name == 'counts': changed['runs'][0]['caseCounts']['pass'] += 1
        if name == 'tool': changed['environment']['GOTOOLCHAIN'] = 'auto'
        if name == 'authority': changed['authoritativeQualification'] = True
        if name == 'whole-done': changed['wholePackageStatus'] = 'DONE'
        if name == 'supplier': changed['qualificationLimits']['actualSupplierPayloadCadence'] = True
        if name == 'provenance': changed['qualificationLimits']['measuredScanUsedDB'] = True
        if name == 'unknown': changed['approved'] = True
        if name == 'missing': del changed['scope']
        if name == 'nested-unknown': changed['qualificationLimits']['approved'] = True
        try:
            validate(changed)
        except ValueError:
            continue
        raise AssertionError('negative fixture accepted: '+name)
    # Alter retained source or gate binding in a temporary receipt, recomputing
    # its outer SHA so the test reaches the inner consistency assertion. Frozen
    # product source and original raw evidence are never changed.
    for name in ('source','gate-log'):
        changed = copy.deepcopy(receipt)
        raw = load_bound(changed['runs'][0])
        if name == 'source':
            first = next(iter(raw['sourceBefore']['sourceSHA256']))
            raw['sourceBefore']['sourceSHA256'][first] = '0' * 64
            raw['sourceAfter']['sourceSHA256'][first] = '0' * 64
        else:
            raw['gates'][0]['logSHA256'] = '0' * 64
        with tempfile.TemporaryDirectory(prefix='.consistency-',dir=ROOT / DIRECTORY) as directory:
            path = Path(directory) / 'changed.json'
            path.write_text(json.dumps(raw))
            changed['runs'][0]['receipt'] = str(path.relative_to(ROOT))
            changed['runs'][0]['sha256'] = digest(changed['runs'][0]['receipt'])
            try:
                validate(changed)
            except ValueError:
                continue
            raise AssertionError('negative fixture accepted: '+name)
    print('signed-update-core evidence consistency: 1 positive, 13 negative assertions PASS')


if __name__ == '__main__':
    main()
