#!/usr/bin/env python3
"""Verify retained local evidence; unsigned receipts never authorize releases."""
import ast
import gzip
import hashlib
import json
import re
import subprocess
from pathlib import Path

FROZEN = '24d9904de4599aa22ec342ae5a2d0bebccdfdb97'
DRIVER = '0d796142cc774c4bede8d934b956e6cf726ddf5635603c538c649b171de76215'
HELPER = '60c3790c8931a5d9986979f9fec5416b1f5e1f697b2c115272db8b40acd8000d'
COSIGN = '4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71'
INDEX_KEYS = {'schemaVersion', 'kind', 'frozenSourceCommit', 'scopedStatus', 'authoritativeQualification', 'wholeW04Status', 'supplierWholeTransfer', 'productionOIDCPublication', 'durableProjection', 'scanUsedWitness', 'artifacts'}
RECEIPT_KEYS = {'cosignSHA256', 'gates', 'guards', 'slice', 'scanUsedWitness', 'supplierWholeTransfer', 'environment', 'actualNodeVersion', 'driverSHA256', 'baselineProductionCommit', 'upstreamSignature', 'schemaVersion', 'productionOIDCPublication', 'frozenSource', 'sourceAfter', 'baselineIsCompatibilityPassControl', 'clusterOrDatabaseExecution', 'baselineProductionPaths', 'measuredDBFreshness', 'wholePackageStatus', 'systemRootSemanticsUnchanged', 'workPackage', 'authoritativeQualification', 'sourceUnchanged', 'helperSHA256', 'overallDeadlineSeconds', 'scopedStatus', 'durableProjection', 'actualGoVersion', 'fullTrackedIndexSHA256', 'baselineRestored', 'sourceSelectionSHA256'}
ROW_KEYS = {'stage', 'originalName', 'path', 'originalBytes', 'originalSHA256', 'storedBytes', 'storedSHA256'}
CLAIMS = ('authoritativeQualification', 'supplierWholeTransfer', 'productionOIDCPublication', 'durableProjection', 'scanUsedWitness')
STAGES = {'sparse-build-failure', 'unsigned-mirror-failure', 'capacity-before-go-failure', 'missing-config-fixture-failure', 'corrected-pass', 'runner-source'}
GATES = ['selected-go-version', 'selected-node-version', 'unchanged-provider-ca-baseline', 'focused-physical-ca-race', 'full-physical-ca-coupled-race', 'original-publisher-signed-graph-readback-race', 'vet', 'json-governance', 'complexity', 'boundaries', 'docs', 'cli-docs']


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def closed_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, 'duplicate JSON key')
        result[key] = value
    return result


def decode(data):
    return json.loads(data, object_pairs_hook=closed_object)


def load_artifacts(root, index):
    require(set(index) == INDEX_KEYS and index['schemaVersion'] == 1 and type(index['schemaVersion']) is int, 'closed index schema')
    require(index['kind'] == 'PinnedDatabaseCALocalEvidence' and index['frozenSourceCommit'] == FROZEN and index['scopedStatus'] == 'PASS' and index['wholeW04Status'] == 'IN_PROGRESS', 'index identity/status')
    require(all(index[key] is False for key in CLAIMS), 'local evidence cannot claim release/runtime authority')
    require(type(index['artifacts']) is list and len(index['artifacts']) == 149, 'complete immutable artifact inventory')
    members = {}
    paths = set()
    total = 0
    for row in index['artifacts']:
        require(type(row) is dict and set(row) == ROW_KEYS and row['stage'] in STAGES, 'closed artifact schema')
        name = row['path']
        require(type(name) is str and not Path(name).is_absolute() and '..' not in Path(name).parts and name not in paths, 'artifact path/duplicate')
        path = root / name
        require(all(not parent.is_symlink() for parent in (path, *path.parents)) and path.resolve().is_relative_to(root.resolve()) and path.is_file(), 'artifact link/containment/regular file')
        paths.add(name)
        require(type(row['storedBytes']) is int and type(row['originalBytes']) is int and 0 <= row['storedBytes'] <= 8 * 1024**2 and 0 <= row['originalBytes'] <= 8 * 1024**2, 'finite artifact sizes')
        stored = path.read_bytes()
        require(len(stored) == row['storedBytes'] and sha(stored) == row['storedSHA256'], 'stored artifact changed')
        if name.endswith('.gz'):
            with gzip.open(path, 'rb') as handle:
                data = handle.read(8 * 1024**2 + 1)
        else:
            data = stored
        total += len(data)
        require(total <= 32 * 1024**2 and len(data) == row['originalBytes'] and sha(data) == row['originalSHA256'], 'original artifact changed/oversized')
        key = (row['stage'], row['originalName'])
        require(key not in members, 'duplicate original member')
        members[key] = data
    require({stage for stage, _ in members} == STAGES, 'missing preserved attempt')
    return members


def check_source(selection, receipt, repository):
    require(set(selection) == {'commit', 'tree', 'selectionMode', 'sparseSelection', 'presentCount', 'missingCount', 'paths'}, 'closed source selection schema')
    require(all(set(row) == {'path', 'gitMode', 'gitBlob', 'present', 'workingSHA256', 'symlinkPayload', 'workingMode'} for row in selection['paths']), 'closed selected row schema')
    require(selection['commit'] == FROZEN and selection['selectionMode'] == 'full-frozen-tracked-checkout' and selection['sparseSelection'] == [] and selection['presentCount'] == 7150 and selection['missingCount'] == 0, 'full frozen source selection')
    require(len(selection['paths']) == 7150 and all(row['present'] is True for row in selection['paths']), 'missing selected source')
    tree = subprocess.check_output(['git', 'rev-parse', FROZEN + '^{tree}'], cwd=repository, timeout=15).decode().strip()
    require(tree == selection['tree'] == receipt['frozenSource']['tree'], 'source tree identity')
    entries = subprocess.check_output(['git', 'ls-tree', '-rz', FROZEN], cwd=repository, timeout=15)
    expected = {}
    for entry in entries.split(b'\0'):
        if entry:
            identity, path = entry.split(b'\t', 1)
            mode, kind, blob = identity.decode().split()
            require(kind == 'blob', 'unexpected tracked object')
            expected[path.decode()] = (mode, blob)
    actual = {row['path']: row for row in selection['paths']}
    require(len(actual) == 7150 and set(actual) == set(expected), 'source paths changed')
    process = subprocess.Popen(['git', 'cat-file', '--batch'], cwd=repository, stdin=subprocess.PIPE, stdout=subprocess.PIPE)
    try:
        for name, (mode, blob) in expected.items():
            row = actual[name]
            require(row['gitMode'] == mode and row['gitBlob'] == blob, 'source mode/blob changed')
            process.stdin.write((blob + '\n').encode()); process.stdin.flush()
            header = process.stdout.readline().decode().split()
            require(len(header) == 3 and header[0] == blob and header[1] == 'blob', 'Git blob response')
            length = int(header[2]); digest = hashlib.sha256(); payload = bytearray()
            while length:
                part = process.stdout.read(min(length, 65536))
                require(bool(part), 'truncated Git blob')
                digest.update(part)
                if mode == '120000':
                    payload.extend(part)
                length -= len(part)
            require(process.stdout.read(1) == b'\n' and digest.hexdigest() == row['workingSHA256'], 'selected bytes differ from frozen Git')
            require((row['workingMode'] & 0o170000) == (0o120000 if mode == '120000' else 0o100000), 'selected file kind')
            require(row['symlinkPayload'] == (payload.decode() if mode == '120000' else None), 'selected symlink payload')
    finally:
        process.stdin.close()
        process.wait(timeout=15)
    for source in [receipt['frozenSource'], receipt['sourceAfter']]:
        require(source['commit'] == FROZEN and source['status'] == '' and source['tree'] == tree, 'frozen status/tree')
        for name, binding in source['files'].items():
            require(name in actual and binding['gitSHA256'] == binding['workingSHA256'] == actual[name]['workingSHA256'], 'runtime source hash changed')
    selected = [(row['path'], 'symlink' if row['gitMode'] == '120000' else 'regular', row['workingMode'], row['workingSHA256']) for row in selection['paths']]
    require(sha(json.dumps(selected, separators=(',', ':')).encode()) == receipt['frozenSource']['selectedWorkingTreeSHA256'], 'full working selection hash')
    staged = b''.join((row['gitMode'] + ' ' + row['gitBlob'] + ' 0\t' + row['path'] + '\0').encode() for row in selection['paths'])
    require(sha(staged) == receipt['fullTrackedIndexSHA256'], 'full tracked index hash')
    require(receipt['frozenSource'] == receipt['sourceAfter'], 'source before/after changed')


def check_receipt(members):
    receipt = decode(members['corrected-pass', 'receipt.json'])
    require(set(receipt) == RECEIPT_KEYS, 'closed receipt schema')
    require(receipt['scopedStatus'] == 'PASS' and receipt['sourceUnchanged'] is True and receipt['baselineRestored'] is True, 'source/restoration/qualification status')
    require(all(receipt[key] is False for key in [*CLAIMS, 'clusterOrDatabaseExecution', 'measuredDBFreshness', 'upstreamSignature']), 'receipt scope inflation')
    require(receipt['driverSHA256'] == DRIVER and receipt['helperSHA256'] == HELPER and receipt['cosignSHA256'] == COSIGN, 'actual selected tools changed')
    require(receipt['actualGoVersion'] == 'go version go1.27.2 linux/amd64' and receipt['actualNodeVersion'] == 'v26.11.1', 'actual tool versions')
    require(receipt['sourceSelectionSHA256'] == sha(members['corrected-pass', 'source-selection.json']), 'selection binding')
    require(sha(members['runner-source', 'plan032-w04-mirrored-ca-config-closed-gates.py']) == DRIVER and sha(members['runner-source', 'plan032_w04_owned_runner.py']) == HELPER, 'retained runner binding')
    require([row['gate'] for row in receipt['gates']] == GATES and len(receipt['guards']) == 53, 'complete exact gates/guards')
    # Compile only the retained pure JSON parser functions, never runner workflow.
    source = ast.parse(members['runner-source', 'plan032-w04-mirrored-ca-config-closed-gates.py'])
    functions = [node for node in source.body if isinstance(node, ast.FunctionDef) and node.name in ('terminals', 'require_packages')]
    focused = [node.value.value for node in ast.walk(source) if isinstance(node, ast.Assign) and any(isinstance(target, ast.Name) and target.id == 'focused' for target in node.targets) and isinstance(node.value, ast.Constant)]
    require(len(focused) == 1, 'retained focused selector')
    focused_names = focused[0].removeprefix('^(').removesuffix(')$').split('|')
    require(len(focused_names) == 20, 'exact focused parents')
    namespace = {'json': json}
    exec(compile(ast.Module(body=functions, type_ignores=[]), '<retained-parser>', 'exec'), namespace)
    import tempfile
    for row in receipt['gates'] + receipt['guards']:
        require(row['exitCode'] == 0 and row['drained'] is True and row['leaderHeldUntilDrain'] is True and row['liveDescendantsAfterDrain'] == [] and not row['reason'] and row['guardException'] is None, 'owned group or gate failure')
        data = members['corrected-pass', row['gate'] + '.log']
        require(sha(data) == row['sha256'], 'raw gate hash binding')
        if row in receipt['guards']:
            match = re.fullmatch(rb'check-build-capacity: path=\. mount=/ available=(\d+)GiB free=(\d+)% required=20GiB/20%\n', data)
            require(match and int(match[1]) >= 20 and int(match[2]) >= 20, 'fatal numeric capacity guard')
        elif '-json' in row['command']:
            with tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / 'events.jsonl'; path.write_bytes(data)
                parsed = namespace['terminals'](path)
            require(all(row[key] == value for key, value in parsed.items()), 'actual terminal counts/identity')
            namespace['require_packages'](parsed, [path for path in row['command'] if path.startswith('./')])
            required = focused_names if row['gate'] == 'focused-physical-ca-race' else [*focused_names, 'TestSelectedCosignProducerToVerifiedLoader', 'TestSelectedCosignDatabaseUpdateToCanonicalLoader'] if row['gate'] == 'full-physical-ca-coupled-race' else ['TestActualPublisherCopiesSignsVerifiesAndRetriesImmutableChildren', 'TestActualPublisherClosesScannerDatabaseSubjectsAndOfflineGraph', 'TestActualPublisherVerifiesDescriptorDatabasePayloadReadback'] if row['gate'] == 'original-publisher-signed-graph-readback-race' else []
            passed = {test['test'] for test in parsed['terminalTests'] if test['action'] == 'pass'}
            require(set(required).issubset(passed), 'actual qualifying fixture missing')
            require(parsed['failures'] == parsed['skips'] == parsed['buildFailures'] == 0 and parsed['terminalPackages'] and all(p['action'] == 'pass' for p in parsed['terminalPackages']), 'actual tests not qualified')
    counts = [(row['parents'], row['cases']) for row in receipt['gates'] if '-json' in row['command']]
    require(counts == [(3, 3), (20, 62), (143, 480), (34, 187)], 'exact race inventory')
    for row in receipt['gates']:
        require(row['sourceBefore'] == row['sourceAfter'], 'source changed during gate')
    for stage in STAGES - {'corrected-pass', 'runner-source'}:
        original = decode(members[stage, 'receipt.json'])
        require(original['scopedStatus'] == 'FAIL' and original['sourceUnchanged'] is True, 'preserved original failure changed')
    return receipt


def verify(root, repository):
    index = decode((root / 'index.json').read_bytes())
    members = load_artifacts(root, index)
    receipt = check_receipt(members)
    check_source(decode(members['corrected-pass', 'source-selection.json']), receipt, repository)
    return index, members


if __name__ == '__main__':
    import sys
    root = Path(__file__).resolve().parent
    repository = Path(sys.argv[1]) if len(sys.argv) > 1 else root.parents[4]
    verify(root, repository)
    print('PASS: 149 retained artifacts, 7150 frozen Git bytes/modes, 12 gates, 53 fatal guards; local evidence only')
