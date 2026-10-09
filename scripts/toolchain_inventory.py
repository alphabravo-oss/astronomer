"""Inventory maintained source pins, separate from fixtures and release identity."""
import pathlib
import re
import subprocess

PATTERNS = {
    'action': re.compile(r'\buses:\s*([^\s@]+@[a-f0-9]{40})'),
    'builder': re.compile(r'^FROM\s+([^\s]+(?::[^\s]+|@sha256:[a-f0-9]{64}))', re.M),
    'go-tool': re.compile(r'\bgo run\s+["\']?([^\s"\']+@v[0-9]+(?:\.[0-9]+){1,2})'),
    'version': re.compile(r'\b(?:[A-Z][A-Z0-9_]*_VERSION|(?:node|go|helm|cosign|syft)-version|cosign-release)\s*[=:]\s*["\']?(v?[0-9]+(?:\.[0-9]+){1,2})'),
    'default': re.compile(r'\$\{(?:[A-Z][A-Z0-9_]*_VERSION|OAPI_CODEGEN_VERSION|GOLANGCI_LINT_VERSION):-([v0-9][0-9.]+)\}'),
    'image': re.compile(r'^\s*(?:image|imageName|k3s_image):\s*["\']?([a-zA-Z0-9][a-zA-Z0-9./_-]*(?::[a-zA-Z0-9_.-]+(?:@sha256:[a-f0-9]{64})?|@sha256:[a-f0-9]{64}))', re.M),
    'tag': re.compile(r'^\s*tag:\s*["\']?(v?[0-9][a-zA-Z0-9_.-]*)', re.M),
}


def paths(root):
    tracked = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=root).decode().split('\0')
    return sorted(path for path in tracked if path and (pathlib.Path(root) / path).is_file() and (path in ('Makefile', 'frontend/Dockerfile') or path.startswith(('.github/', 'deploy/', 'scripts/', 'tools/', 'docs/'))) and not path.startswith(('deploy/toolchain/', 'docs/assurance/', 'scripts/pinned/', 'scripts/testdata/')) and not any(word in path for word in ('node_modules/', 'package-lock.json', '_test.', '.test.', '/tests/')) and not path.endswith(('.json', '.sum', '.lock', '.svg', '.md', '.html', '.css')))


def scan(root, source_paths=None):
    result = []
    for path in source_paths if source_paths is not None else paths(root):
        source = (pathlib.Path(root) / path).read_text()
        for kind, pattern in PATTERNS.items():
            for match in pattern.finditer(source):
                result.append({'path': path, 'kind': kind, 'value': match.group(1), 'start': match.start(), 'end': match.end()})
    return result


def key(row):
    return row['path'], row['kind'], row['value']


def check(document, root):
    actual = {key(row) for row in scan(root)}
    declared = {key(row) for row in document['inventory']}
    if len(declared) != len(document['inventory']):
        raise ValueError('duplicate source inventory pin')
    if actual != declared:
        added = sorted(actual - declared)
        removed = sorted(declared - actual)
        raise ValueError(f'uninventoried/changed source pins: {added}; obsolete inventory: {removed}')
    ids = {c['id'] for c in document['components']}
    for row in document['inventory']:
        if row['state'] == 'managed' and row.get('component') not in ids:
            raise ValueError('inventory refers to unknown authority component')
