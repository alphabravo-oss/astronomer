#!/usr/bin/env python3
"""Retain every secret finding; match only reviewed immutable historical sites."""
import argparse
import datetime as dt
import gzip
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import tempfile
import tomllib

MAX_BYTES = 64 << 20
SITE_KEYS = ('Commit', 'File', 'RuleID', 'Fingerprint', 'StartLine', 'EndLine', 'StartColumn', 'EndColumn')
CLASSES = {
    'verified-public-file-digest', 'public-locked-module-checksum',
    'public-git-commit', 'public-build-vcsrevision',
    'repository-uuid-test-log', 'task-uuid-test-log', 'backend-uuid-test-log',
    'literal-test-case-name', 'fixed-runtime-version-negative-fixture',
}
MANIFEST_KEYS = {'schema_version', 'record_count', 'registry_gzip_sha256', 'registry_json_sha256',
                 'original_report_gzip_sha256', 'original_report_json_sha256',
                 'site_evidence_sha256', 'column_evidence_sha256', 'column_replay_sha256', 'scanner',
                 'review_owner', 'review_evidence', 'expires_at', 'release_scope', 'inputs'}
CONTROL_KEYS = {'index', 'site', 'blob_sha256', 'classification', 'proof', 'proof_sha256',
                'column_proof', 'column_proof_sha256'}
COLLECTION_KEYS = {'schema_version', 'scanner_sha256', 'scanner_module', 'config_sha256',
                   'exit_code', 'redact_percent', 'log_opts', 'report_sha256',
                   'refs_before_sha256', 'refs_after_sha256', 'source_before_sha256',
                   'source_after_sha256', 'collection_complete'}

def require(condition):
    if not condition:
        raise ValueError('invalid or drifted retained-history security evidence')

def sha(data):
    return hashlib.sha256(data).hexdigest()

def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()

def pairs(rows):
    result = {}
    for key, value in rows:
        require(key not in result)
        result[key] = value
    return result

def parse(data):
    require(len(data) <= MAX_BYTES)
    return json.loads(data, object_pairs_hook=pairs)

def bounded_bytes(path):
    with Path(path).open('rb') as stream:
        data = stream.read(MAX_BYTES + 1)
    require(len(data) <= MAX_BYTES)
    return data

def valid_sha(value):
    return isinstance(value, str) and re.fullmatch(r'[0-9a-f]{64}', value) is not None

def site_tuple(site):
    require(isinstance(site, dict) and set(site) == set(SITE_KEYS))
    require(isinstance(site['Commit'], str) and re.fullmatch(r'[0-9a-f]{40}', site['Commit']))
    path = site['File']
    require(isinstance(path, str) and path and not PurePosixPath(path).is_absolute()
            and '..' not in PurePosixPath(path).parts and ':' not in path and '\\' not in path)
    require(all(isinstance(site[k], str) and site[k] for k in ('RuleID', 'Fingerprint')))
    require(all(type(site[k]) is int and site[k] > 0 for k in SITE_KEYS[4:]))
    require(site['StartLine'] <= site['EndLine'])
    return tuple(site[k] for k in SITE_KEYS)

def load_registry(manifest, gzip_path, scope, now, evidence_root):
    require(isinstance(manifest, dict) and set(manifest) == MANIFEST_KEYS)
    require(type(manifest['schema_version']) is int and manifest['schema_version'] == 1)
    require(type(manifest['record_count']) is int and manifest['record_count'] > 0)
    require(all(valid_sha(manifest[key]) for key in MANIFEST_KEYS if key.endswith('_sha256')))
    require(manifest['release_scope'] == scope and isinstance(manifest['review_owner'], str) and manifest['review_owner'])
    require(isinstance(manifest['review_evidence'], list) and manifest['review_evidence'])
    require(all(set(row) == {'path', 'sha256'} and isinstance(row['path'], str) and valid_sha(row['sha256']) for row in manifest['review_evidence']))
    expiry = dt.datetime.fromisoformat(manifest['expires_at'].replace('Z', '+00:00'))
    require(expiry.tzinfo is not None and expiry.utcoffset() == dt.timedelta(0) and expiry > now)
    scanner = manifest['scanner']
    require(set(scanner) == {'module', 'executable_sha256', 'config_sha256', 'column_source_sha256'})
    require(scanner['module'] == 'github.com/zricethezav/gitleaks/v8 v8.30.1')
    require(valid_sha(scanner['executable_sha256']) and valid_sha(scanner['config_sha256']))
    require(isinstance(scanner['column_source_sha256'], dict) and scanner['column_source_sha256']
            and all(isinstance(k, str) and valid_sha(v) for k, v in scanner['column_source_sha256'].items()))
    compressed = bounded_bytes(gzip_path)
    require(sha(compressed) == manifest['registry_gzip_sha256'])
    with gzip.GzipFile(fileobj=__import__('io').BytesIO(compressed)) as stream:
        raw = stream.read(MAX_BYTES + 1)
    require(len(raw) <= MAX_BYTES and sha(raw) == manifest['registry_json_sha256'])
    registry = parse(raw)
    require(set(registry) == {'schema_version', 'records'} and type(registry['schema_version']) is int and registry['schema_version'] == 1)
    require(isinstance(registry['records'], list) and len(registry['records']) == manifest['record_count'])
    inputs = verify_inputs(manifest, evidence_root)
    originals, sites, columns, replay = inputs
    require(len(originals) == len(sites['records']) == manifest['record_count'])
    require(isinstance(replay['records'], list) and len(replay['records']) == len(originals))
    supplements = {row['index']: row for row in columns['records']}
    require(len(supplements) == len(columns['records']))
    controls = {}
    for index, row in enumerate(registry['records']):
        require(set(row) == CONTROL_KEYS and type(row['index']) is int and row['index'] == index)
        key = site_tuple(row['site'])
        require(key not in controls and row['classification'] in CLASSES and valid_sha(row['blob_sha256']))
        require(isinstance(row['proof'], dict) and sha(canonical(row['proof'])) == row['proof_sha256'])
        require(isinstance(row['column_proof'], dict) and sha(canonical(row['column_proof'])) == row['column_proof_sha256'])
        original, supplied = originals[index], sites['records'][index]
        require({k: original[k] for k in SITE_KEYS} == row['site'])
        require(supplied['index'] == index and supplied['fingerprint'] == row['site']['Fingerprint']
                and supplied['commit'] == row['site']['Commit'] and supplied['path'] == row['site']['File']
                and supplied['rule'] == row['site']['RuleID'] and supplied['line'] == row['site']['StartLine']
                and supplied['column_start'] == row['site']['StartColumn'] and supplied['column_end'] == row['site']['EndColumn']
                and supplied['historical_blob_sha256'] == row['blob_sha256']
                and supplied['classification'] == row['classification'] and supplied['proof'] == row['proof'])
        capture = replay['records'][index]
        require(capture['index'] == index and capture['fingerprint'] == row['site']['Fingerprint']
                and capture['historical_blob_sha256'] == row['blob_sha256'])
        cp = capture['proof']
        require(row['column_proof']['actual_blob_columns'] == cp['actual_blob_columns']
                and row['column_proof']['replay_row_sha256'] == sha(canonical(capture)))
        if index in supplements:
            supplied_column = supplements[index]
            require(supplied_column['fingerprint'] == row['site']['Fingerprint']
                    and supplied_column['historical_blob_sha256'] == row['blob_sha256'])
            prior_columns = supplied_column.get('exact_rule_match_columns') or supplied_column['proof']['actual_blob_match_columns']
            require(cp['actual_blob_columns'] == prior_columns)
        row['_capture_proof'] = cp
        controls[key] = row
    return controls

def artifact(root, descriptor):
    require(isinstance(descriptor, dict) and set(descriptor) == {'path', 'sha256'})
    relative = descriptor['path']
    require(isinstance(relative, str) and relative and not PurePosixPath(relative).is_absolute()
            and '..' not in PurePosixPath(relative).parts and valid_sha(descriptor['sha256']))
    base = Path(root).resolve()
    path = base / relative
    require(path.resolve().is_relative_to(base) and not path.is_symlink())
    data = bounded_bytes(path)
    require(sha(data) == descriptor['sha256'])
    return data

def unpack(compressed):
    with gzip.GzipFile(fileobj=__import__('io').BytesIO(compressed)) as stream:
        raw = stream.read(MAX_BYTES + 1)
    require(len(raw) <= MAX_BYTES)
    return raw

def verify_inputs(manifest, root):
    inputs = manifest['inputs']
    require(isinstance(inputs, dict) and set(inputs) == {'original_report', 'site_evidence', 'column_evidence', 'column_replay', 'column_sources'})
    report_gzip = artifact(root, inputs['original_report'])
    require(sha(report_gzip) == manifest['original_report_gzip_sha256'])
    report_raw = unpack(report_gzip)
    require(sha(report_raw) == manifest['original_report_json_sha256'])
    site_raw = artifact(root, inputs['site_evidence'])
    column_raw = artifact(root, inputs['column_evidence'])
    require(sha(site_raw) == manifest['site_evidence_sha256'] and sha(column_raw) == manifest['column_evidence_sha256'])
    replay_raw = artifact(root, inputs['column_replay'])
    require(sha(replay_raw) == manifest['column_replay_sha256'])
    sites, columns, replay = parse(site_raw), parse(column_raw), parse(replay_raw)
    require(sites['report_compressed_sha256'] == sha(report_gzip)
            and columns['report_compressed_sha256'] == sha(report_gzip)
            and columns['report_raw_sha256'] == sha(report_raw)
            and replay['report_compressed_sha256'] == sha(report_gzip) and replay['report_raw_sha256'] == sha(report_raw))
    sources = inputs['column_sources']
    require(isinstance(sources, dict) and set(sources) == set(manifest['scanner']['column_source_sha256'])
            and all(manifest['scanner']['column_source_sha256'].get(k) == v for k, v in columns['source_proof_hashes'].items())
            and replay['source_proof_hashes'] == manifest['scanner']['column_source_sha256'])
    for name, descriptor in sources.items():
        require(sha(artifact(root, descriptor)) == manifest['scanner']['column_source_sha256'][name])
    for review in manifest['review_evidence']:
        artifact(root, review)
    return parse(report_raw), sites, columns, replay

class GitEvidence:
    def __init__(self, root, rule_config):
        self.root, self.cache = Path(root), {}
        self.rule_config_sha256 = sha(bounded_bytes(rule_config))
        config = tomllib.loads(bounded_bytes(rule_config).decode())
        self.patterns = {row['id']: re.compile(row['regex']) for row in config['rules']
                         if row['id'] in {'generic-api-key', 'sourcegraph-access-token'}}
        require(set(self.patterns) == {'generic-api-key', 'sourcegraph-access-token'})

    def output(self, args, limit):
        # File-backed output prevents allocation before bounds validation. Git's
        # immutable object size is checked before requesting the blob bytes.
        with tempfile.TemporaryFile() as output:
            result = subprocess.run(['git', *args], cwd=self.root, stdout=output,
                                    stderr=subprocess.DEVNULL, timeout=15)
            require(result.returncode == 0)
            output.seek(0)
            data = output.read(limit + 1)
            require(len(data) <= limit)
            return data

    def blob(self, commit, path):
        site_tuple(dict(Commit=commit, File=path, RuleID='evidence', Fingerprint='evidence',
                        StartLine=1, EndLine=1, StartColumn=1, EndColumn=1))
        key = commit, path
        if key not in self.cache:
            size_raw = self.output(['cat-file', '-s', commit + ':' + path], 32)
            require(re.fullmatch(rb'[0-9]+\n', size_raw) is not None)
            size = int(size_raw)
            require(size <= MAX_BYTES)
            data = self.output(['cat-file', 'blob', commit + ':' + path], size)
            require(len(data) == size)
            self.cache[key] = data
        return self.cache[key]

    def is_commit(self, value):
        require(isinstance(value, str) and re.fullmatch(r'[0-9a-f]{40}', value))
        return self.output(['cat-file', '-t', value], 16) == b'commit\n'

    def capture(self, site, line, start, end):
        require(site['RuleID'] in self.patterns)
        matches = [match for match in self.patterns[site['RuleID']].finditer(line)
                   if match.start() + 1 == start and match.end() == end]
        require(len(matches) == 1)
        return matches[0]


def pointer(value, components):
    for key in components:
        value = value[int(key)] if isinstance(value, list) else value[key]
    return value

def scalar_span(raw, components):
    """Locate the exact JSON scalar identified by an already validated pointer."""
    text = raw.decode()
    parse(raw)
    decoder = json.JSONDecoder()
    position, spans = 0, {}
    def whitespace():
        nonlocal position
        while position < len(text) and text[position].isspace(): position += 1
    def read(path):
        nonlocal position
        whitespace()
        if text[position] == '{':
            position += 1; whitespace()
            if text[position] != '}':
                while True:
                    whitespace(); key, finish = decoder.raw_decode(text, position)
                    require(isinstance(key, str)); position = finish; whitespace()
                    require(text[position] == ':'); position += 1; read(path + [key]); whitespace()
                    if text[position] == '}': break
                    require(text[position] == ','); position += 1
            position += 1
        elif text[position] == '[':
            position += 1; whitespace(); index = 0
            if text[position] != ']':
                while True:
                    read(path + [str(index)]); index += 1; whitespace()
                    if text[position] == ']': break
                    require(text[position] == ','); position += 1
            position += 1
        else:
            start = position
            value, position = decoder.raw_decode(text, position)
            spans[tuple(path)] = (start, position, value)
    read([]); whitespace(); require(position == len(text))
    require(tuple(components) in spans)
    return spans[tuple(components)]

def bind_scalar(blob, components, site, match):
    start, end, value = scalar_span(blob, components)
    text = blob.decode()
    line_start = sum(len(line) for line in text.splitlines(keepends=True)[:site['StartLine'] - 1])
    token_start, token_end = match.span(1)
    require(start <= line_start + token_start < line_start + token_end <= end)
    return value

def verify_proof(row, evidence):
    site, proof, kind = row['site'], row['proof'], row['classification']
    blob = evidence.blob(site['Commit'], site['File'])
    require(sha(blob) == row['blob_sha256'])
    lines = blob.decode().splitlines()
    require(site['EndLine'] <= len(lines))
    line = lines[site['StartLine'] - 1]
    column = row['column_proof']
    require(set(column) == {'actual_blob_columns', 'span_sha256', 'reviewed_origin', 'replay_row_sha256'})
    require(isinstance(column['actual_blob_columns'], list) and len(column['actual_blob_columns']) == 2)
    start, end = column['actual_blob_columns']
    require(type(start) is int and type(end) is int and 0 < start <= end <= len(line))
    require(sha(line[start - 1:end].encode()) == column['span_sha256'])
    require(isinstance(column['reviewed_origin'], str) and column['reviewed_origin'])
    require(site['StartLine'] == site['EndLine'])
    match = evidence.capture(site, line, start, end)
    token = match[1]
    if '_capture_proof' in row:
        cp = row['_capture_proof']
        require(cp['token_columns'] == [match.start(1)+1, match.end(1)] and cp['token_sha256'] == sha(token.encode()))
    if kind == 'verified-public-file-digest':
        require(set(proof) == {'field', 'referenced_path', 'referenced_commit', 'referenced_blob_sha256', 'field_components', 'field_rfc6901'})
        reference = evidence.blob(proof['referenced_commit'], proof['referenced_path'])
        require(sha(reference) == proof['referenced_blob_sha256'])
        require(proof['field_rfc6901'] == '/' + '/'.join(str(c).replace('~', '~0').replace('/', '~1') for c in proof['field_components']))
        require(bind_scalar(blob, proof['field_components'], site, match) == sha(reference) == token)
    elif kind == 'public-locked-module-checksum':
        require(set(proof) == {'module', 'version', 'field', 'go_sum_path', 'go_sum_sha256'})
        sums = evidence.blob(site['Commit'], proof['go_sum_path'])
        require(sha(sums) == proof['go_sum_sha256'])
        fields = re.search(r'\bdep\s+(\S+)\s+(v\S+)\s+(h1:\S+)', line)
        require(fields is not None and fields[1] == proof['module'] and fields[2] == proof['version'])
        require((' '.join(fields.groups())).encode() in sums.splitlines())
        require(match.span(1) == (fields.start(3) + 3, fields.end(3)) and token == fields[3][3:])
    elif kind in {'public-git-commit', 'public-build-vcsrevision'}:
        expected = {'field', 'git_commit_object_exists', 'source_has_sgp_token'} if kind == 'public-git-commit' else {'field', 'git_commit_object_exists', 'no_sgp_token'}
        require(set(proof) == expected and proof['git_commit_object_exists'] is True)
        components = [c.replace('~1', '/').replace('~0', '~') for c in proof['field'].strip('/').split('/')]
        value = bind_scalar(blob, components, site, match)
        if kind == 'public-build-vcsrevision':
            matches = re.findall(r'\bvcs.revision=([0-9a-f]{40})', value)
            require(len(matches) == 1 and proof['no_sgp_token'] is True)
            value = matches[0]
        else:
            require(proof['source_has_sgp_token'] is False)
        require(value == token and evidence.is_commit(value))
    elif kind == 'fixed-runtime-version-negative-fixture':
        require(set(proof) == {'function', 'field', 'source_sha256'})
        require(proof['source_sha256'] == sha(blob) and proof['function'] == 'test_runtime_variants_do_not_accept_prefix_or_arbitrary_suffix'
                and proof['field'] == 'literal iteration table name/bad_tag')
        import ast
        nodes = [n for n in ast.walk(ast.parse(blob.decode())) if isinstance(n, ast.FunctionDef)
                 and n.name == proof['function'] and n.lineno <= site['StartLine'] <= n.end_lineno]
        require(len(nodes) == 1)
        cells = [cell for node in ast.walk(nodes[0]) if isinstance(node, ast.Tuple) and len(node.elts) == 2
                 and all(isinstance(c, ast.Constant) and isinstance(c.value, str) for c in node.elts)
                 for cell in node.elts if cell.lineno == site['StartLine']
                 and cell.col_offset < match.start(1) < match.end(1) <= cell.end_col_offset - 1]
        require(len(cells) == 1 and token in cells[0].value)
    else:
        expected = {'test', 'producer_sources'} if kind == 'literal-test-case-name' else {'field', 'go_test_event', 'test', 'producer_sources'}
        require(set(proof) == expected and isinstance(proof['producer_sources'], list) and proof['producer_sources'])
        for producer in proof['producer_sources']:
            require(set(producer) == {'path', 'commit', 'sha256', 'lines', 'static_statement'})
            data = evidence.blob(producer['commit'], producer['path'])
            require(sha(data) == producer['sha256'])
            producer_lines = data.decode().splitlines()
            require(producer['lines'] and all(type(n) is int and 0 < n <= len(producer_lines)
                    and producer['static_statement'] in producer_lines[n - 1] for n in producer['lines']))
        if kind == 'literal-test-case-name':
            event = parse(line.encode())
            require(event['Test'] == proof['test'] and '--target-key=api' in proof['test']
                    and proof['test'].count(token) == 1)
            scalar_start, scalar_end, _ = scalar_span(line.encode(), ['Test'])
            if not scalar_start <= match.start(1) < match.end(1) <= scalar_end:
                output = event.get('Output')
                require(event.get('Action') == 'output' and isinstance(output, str))
                prefix = '=== RUN   ' if output.startswith('=== RUN   ') else '--- PASS: '
                expected = prefix + proof['test']
                require(output.startswith(expected) and re.fullmatch(r'(?:\n| \([0-9]+(?:\.[0-9]+)?s\)\n)', output[len(expected):]))
                scalar_start, _, _ = scalar_span(line.encode(), ['Output'])
                before_token = prefix + proof['test'][:proof['test'].index(token)]
                require(match.start(1) == scalar_start + 1 + len(json.dumps(before_token, ensure_ascii=False)[1:-1]))
        else:
            field = {'repository-uuid-test-log': 'repository_id', 'task-uuid-test-log': 'task_id', 'backend-uuid-test-log': 'backend_id'}[kind]
            require(proof['field'] == 'Output.' + field and type(proof['go_test_event']) is bool)
            text = line
            if proof['go_test_event']:
                event = parse(line)
                require(event['Test'] == proof['test'])
                text = event['Output']
            found = re.search(r'\b' + field + r'=([0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12})(?:\s|$)', text)
            require(found is not None and token == found[1])
            if proof['go_test_event']:
                require(bind_scalar(line.encode(), ['Output'], dict(site, StartLine=1), match) == text)
                # Bind the captured UUID to this particular labelled occurrence,
                # rather than an equal UUID in another field on the same line.
                encoded_prefix = json.dumps(text[:found.start(1)], ensure_ascii=False)[1:-1]
                scalar_start, _, _ = scalar_span(line.encode(), ['Output'])
                require(match.start(1) == scalar_start + 1 + len(encoded_prefix))
            else:
                require(match.span(1) == found.span(1))

def evaluate(report_raw, collection, manifest, controls, scope, evidence):
    require(set(collection) == COLLECTION_KEYS and type(collection['schema_version']) is int and collection['schema_version'] == 1)
    require(type(collection['exit_code']) is int and collection['exit_code'] in (0, 42)
            and type(collection['redact_percent']) is int and collection['redact_percent'] == 100
            and collection['log_opts'] == '--all' and collection['collection_complete'] is True)
    scanner = manifest['scanner']
    configs = [v for k, v in scanner['column_source_sha256'].items() if k.endswith('/config/gitleaks.toml')]
    require(configs == [evidence.rule_config_sha256])
    require(collection['scanner_sha256'] == scanner['executable_sha256']
            and collection['scanner_module'] == scanner['module'] and collection['config_sha256'] == scanner['config_sha256'])
    require(collection['report_sha256'] == sha(report_raw))
    require(all(valid_sha(collection[key]) for key in COLLECTION_KEYS if key.endswith('_sha256')))
    require(collection['refs_before_sha256'] == collection['refs_after_sha256']
            and collection['source_before_sha256'] == collection['source_after_sha256'])
    report = parse(report_raw)
    require(isinstance(report, list) and (bool(report) == (collection['exit_code'] == 42)))
    findings, seen, accepted = [], set(), set()
    for index, finding in enumerate(report):
        require(isinstance(finding, dict) and all(key in finding for key in SITE_KEYS))
        require(finding.get('Secret') == 'REDACTED' and isinstance(finding.get('Match'), str) and 'REDACTED' in finding['Match'])
        site = {key: finding[key] for key in SITE_KEYS}
        key = site_tuple(site)
        require(key not in seen)
        seen.add(key)
        control = controls.get(key)
        if control is not None:
            verify_proof(control, evidence)
            accepted.add(key)
        findings.append({'index': index, 'site': site, 'accepted': control is not None,
                         'classification': control['classification'] if control else None})
    missing = len(set(controls) - seen)
    unaccepted = len(findings) - len(accepted)
    return {'schema_version': 1, 'release_scope': scope, 'passed': missing == unaccepted == 0,
            'missing_reviewed_sites': missing, 'unaccepted_count': unaccepted,
            'finding_count': len(findings), 'accepted_count': len(accepted), 'findings': findings,
            'approval_scope': 'Static exact historical-site review; no human release approval is inferred'}

def retain_unaccepted(report_raw, scope):
    """Malformed policy/provenance must not erase an otherwise valid report."""
    report = parse(report_raw)
    require(isinstance(report, list))
    findings = []
    for index, finding in enumerate(report):
        require(isinstance(finding, dict) and finding.get('Secret') == 'REDACTED'
                and isinstance(finding.get('Match'), str) and 'REDACTED' in finding['Match'])
        site = {key: finding[key] for key in SITE_KEYS}
        site_tuple(site)
        findings.append({'index': index, 'site': site, 'accepted': False, 'classification': None})
    return {'schema_version': 1, 'release_scope': scope, 'passed': False,
            'finding_count': len(findings), 'accepted_count': 0, 'unaccepted_count': len(findings),
            'findings': findings, 'failure_category': 'invalid_or_unavailable_history_evidence'}

def main():
    parser = argparse.ArgumentParser()
    for name in ('report', 'collection', 'manifest', 'registry', 'output', 'repo', 'evidence-root', 'rule-config'):
        parser.add_argument('--' + name, type=Path, required=True)
    parser.add_argument('--scope', required=True)
    args = parser.parse_args()
    try:
        manifest = parse(bounded_bytes(args.manifest))
        controls = load_registry(manifest, args.registry, args.scope, dt.datetime.now(dt.timezone.utc), args.evidence_root)
        summary = evaluate(bounded_bytes(args.report), parse(bounded_bytes(args.collection)),
                           manifest, controls, args.scope, GitEvidence(args.repo, args.rule_config))
    except (ValueError, KeyError, TypeError, AttributeError, OSError, UnicodeError, subprocess.SubprocessError):
        try:
            summary = retain_unaccepted(bounded_bytes(args.report), args.scope)
        except (ValueError, KeyError, TypeError, AttributeError, OSError, UnicodeError):
            summary = {'schema_version': 1, 'release_scope': args.scope, 'passed': False,
                       'failure_category': 'invalid_or_unavailable_history_evidence'}
    with args.output.open('x') as stream:
        json.dump(summary, stream, indent=2)
        stream.write('\n')
    print('Retained-history security policy: ' + ('PASS' if summary['passed'] else 'FAIL'))
    return 0 if summary['passed'] else 1

if __name__ == '__main__':
    raise SystemExit(main())
