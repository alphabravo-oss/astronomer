#!/usr/bin/env python3
"""Check local source/log consistency, never grant release qualification."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[4]
DIRECTORY = Path('docs/assurance/technology-guide/w03')
FIXTURES = ('TestSelectedCosignProducerToVerifiedLoader', 'TestActualPublisherCopiesSignsVerifiesAndRetriesImmutableChildren', 'TestActualPublisherClosesScannerDatabaseSubjectsAndOfflineGraph', 'TestActualPublisherVerifiesDescriptorDatabasePayloadReadback')


def owned(path):
    relative = Path(path)
    if relative.is_absolute() or '..' in relative.parts or (ROOT / relative).is_symlink():
        raise ValueError('unowned evidence path')
    resolved = (ROOT / relative).resolve(strict=True)
    resolved.relative_to(ROOT)
    if not resolved.is_file():
        raise ValueError('evidence path not regular')
    return resolved


def digest(path):
    return hashlib.sha256(owned(path).read_bytes()).hexdigest()


def events(path):
    rows = []
    for line in owned(path).read_text().splitlines():
        try:
            rows.append(json.loads(line))
        except json.JSONDecodeError:
            continue  # Go may emit a build diagnostic outside test JSON.
    return rows


def validate(receipt):
    allowed = {'schemaVersion','workPackage','slice','wholePackageStatus','authoritativeQualification','sourceCommit','sourceTreeGitObject','sourceSHA256','sourceBeforeAfterByteEqual','rawGateResults','environment','PATHPrefix','cosignSHA256','gates','raceLog','observedCounts','qualificationLimits','recordedAt','postEvidenceDocs'}
    limits = {'supplierWholePayload','databaseFreshness','liveNoEgress','productionOIDC','candidateStartup','releaseOrFutureTagApproval','registryImportReadback','fullNetworkCLIExportSuccess','qaKeyIdentity','executedCLI','sourceSecurityOverall'}
    if set(receipt) != allowed or set(receipt['qualificationLimits']) != limits or set(receipt['environment']) != {'GOTOOLCHAIN','GOMAXPROCS','GOFLAGS','COSIGN_BIN','NODE_PATH'}:
        raise ValueError('open/missing receipt fields')
    if receipt['schemaVersion'] != 1 or receipt['workPackage'] != 'W03' or receipt['wholePackageStatus'] != 'IN_PROGRESS' or receipt['authoritativeQualification'] is not False:
        raise ValueError('invalid local scope/authority')
    tree = subprocess.check_output(['git', 'rev-parse', receipt['sourceCommit'] + '^{tree}'], cwd=ROOT, text=True).strip()
    if tree != receipt['sourceTreeGitObject']:
        raise ValueError('changed source tree identity')
    for path, expected in receipt['sourceSHA256'].items():
        if digest(path) != expected:
            raise ValueError('changed source bytes')
    raw = receipt['rawGateResults']
    if digest(raw['path']) != raw['sha256']:
        raise ValueError('changed command/environment record')
    recorded = json.loads(owned(raw['path']).read_text())
    if recorded['sourceBefore'] != recorded['sourceAfter'] or recorded['sourceBefore']['commit'] != receipt['sourceCommit'] or recorded['sourceBefore']['sourceSHA256'] != receipt['sourceSHA256']:
        raise ValueError('source before/after mismatch')
    if recorded['gates'] != receipt['gates'] or len(receipt['gates']) != 11:
        raise ValueError('changed gate set')
    for gate in receipt['gates']:
        if gate['exitCode'] != 0 or digest(gate['retainedLog']) != gate['logSHA256']:
            raise ValueError('failed/changed gate log')
    final_docs = receipt['postEvidenceDocs']
    if set(final_docs) != {'command','exitCode','retainedLog','logSHA256','runtime','scope'} or final_docs['command'] != 'node scripts/check-docs.mjs' or final_docs['exitCode'] != 0 or digest(final_docs['retainedLog']) != final_docs['logSHA256']:
        raise ValueError('failed/changed final documentation log')
    rows = events(receipt['raceLog'])
    cases = {kind: sum(row.get('Action') == kind and bool(row.get('Test')) for row in rows) for kind in ('pass', 'fail', 'skip')}
    parents = sum(row.get('Action') == 'pass' and bool(row.get('Test')) and '/' not in row['Test'] for row in rows)
    packages = {row['Package']: row['Elapsed'] for row in rows if row.get('Action') == 'pass' and not row.get('Test')}
    if cases != receipt['observedCounts']['cases'] or cases != {'pass': 656, 'fail': 0, 'skip': 0} or parents != 168 or parents != receipt['observedCounts']['parents'] or len(packages) != 7:
        raise ValueError('actual race counts changed')
    for fixture in FIXTURES:
        found = [row for row in rows if row.get('Test') == fixture and row.get('Action') in ('pass', 'skip', 'fail')]
        if len(found) != 1 or found[0]['Action'] != 'pass':
            raise ValueError('selected producer fixture did not execute')
    if receipt['environment']['COSIGN_BIN'] != '/root/.cache/astronomer-032/toolchains/bin/cosign-3.1.3' or receipt['environment']['GOTOOLCHAIN'] != 'go1.27.2' or receipt['environment']['GOFLAGS'] != '-p=2' or receipt['environment']['GOMAXPROCS'] != '4':
        raise ValueError('selected tool invocation changed')
    if hashlib.sha256(Path(receipt['environment']['COSIGN_BIN']).read_bytes()).hexdigest() != receipt['cosignSHA256']:
        raise ValueError('selected producer bytes changed')
    unqualified = {'supplierWholePayload','databaseFreshness','liveNoEgress','productionOIDC','candidateStartup','releaseOrFutureTagApproval','registryImportReadback','fullNetworkCLIExportSuccess'}
    if any(receipt['qualificationLimits'][field] is not False for field in unqualified):
        raise ValueError('unearned external qualification')


def main():
    receipt = json.loads((ROOT / DIRECTORY / 'database-export-cli-evidence.json').read_text())
    validate(receipt)
    for name in ('tree', 'source', 'gate-log', 'counts', 'authority', 'whole-done', 'supplier', 'tool', 'unknown', 'missing', 'nested-unknown'):
        changed = copy.deepcopy(receipt)
        if name == 'tree': changed['sourceTreeGitObject'] = '0' * 40
        if name == 'source': changed['sourceSHA256'][next(iter(changed['sourceSHA256']))] = '0' * 64
        if name == 'gate-log': changed['gates'][0]['logSHA256'] = '0' * 64
        if name == 'counts': changed['observedCounts']['cases']['pass'] += 1
        if name == 'authority': changed['authoritativeQualification'] = True
        if name == 'whole-done': changed['wholePackageStatus'] = 'DONE'
        if name == 'supplier': changed['qualificationLimits']['supplierWholePayload'] = True
        if name == 'tool': changed['environment']['GOTOOLCHAIN'] = 'auto'
        if name == 'unknown': changed['qualification'] = 'green'
        if name == 'missing': del changed['slice']
        if name == 'nested-unknown': changed['qualificationLimits']['approved'] = True
        try:
            validate(changed)
        except ValueError:
            continue
        raise AssertionError('negative fixture accepted: ' + name)
    print('database-export-cli evidence consistency: 1 positive, 11 negative assertions PASS')


if __name__ == '__main__':
    main()
