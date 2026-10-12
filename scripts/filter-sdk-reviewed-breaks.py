#!/usr/bin/env python3
"""Apply exact reviewed v2 response migrations to the v1 SDK compile fixture."""
import hashlib
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parent.parent


def response_field(text, response, field):
    match = re.search(r'type ' + re.escape(response) + r' struct \{\n(.*?)\n\}', text, re.S)
    if match is None:
        raise ValueError('reviewed response missing: ' + response)
    start = re.search(r'^\t' + re.escape(field) + r'\s+(.*)', match[1], re.M)
    if start is None:
        raise ValueError('reviewed response field missing: ' + response + '.' + field)
    lines = match[1][start.start():].splitlines(keepends=True)
    count, depth = 0, 0
    for line in lines:
        count += len(line)
        depth += line.count('{') - line.count('}')
        if depth == 0:
            break
    if depth != 0:
        raise ValueError('unbalanced response field')
    begin = match.start(1) + start.start()
    return begin, begin + count, text[begin:begin + count]


def field_type(declaration, field):
    return re.sub(r'\s+', '', declaration.removeprefix('\t' + field))


def reviewed_baseline(baseline, current, review):
    if hashlib.sha256(baseline).hexdigest() != review['baseline_sha256']:
        raise ValueError('SDK baseline differs from reviewed v1.2.1 source')
    if hashlib.sha256(current).hexdigest() != review['current_sha256']:
        raise ValueError('SDK candidate differs from reviewed v2 source')
    text = baseline.decode()
    removed = 0
    for name, fields in review['removed_fields'].items():
        if fields != ['JSON200']:
            raise ValueError('only reviewed tool JSON200 fields may be removed')
        pattern = r'(type ' + re.escape(name) + r' struct \{\n)(.*?)(\n\})'
        match = re.search(pattern, text, re.S)
        if match is None:
            raise ValueError('reviewed SDK response type missing: ' + name)
        body, count = re.subn(r'^\s*JSON200[^\n]*\n', '', match[2], flags=re.M)
        if count != 1:
            raise ValueError('reviewed SDK field missing or ambiguous: ' + name)
        text = text[:match.start()] + match[1] + body + match[3] + text[match.end():]
        removed += count
    renames = review.get('renamed_types', {})
    for old, new in renames.items():
        if (old != 'ListCharlieSessions200Mode' or new != 'ListCharlieSessions200DataMode'
                or text.count('type ' + old + ' string') != 1
                or current.decode().count('type ' + new + ' string') != 1
                or re.search(r'\b' + old + r'\b', current.decode())):
            raise ValueError('unreviewed SDK type rename')
    for name, fields in review.get('wrapped_fields', {}).items():
        if 'Charlie' not in name or fields not in (['JSON200'], ['JSON201'], ['JSON202']):
            raise ValueError('only reviewed Charlie success payloads may be wrapped')
        field = fields[0]
        begin, end, before = response_field(text, name, field)
        _, _, after = response_field(current.decode(), name, field)
        before_type = field_type(before, field)
        for old, new in renames.items():
            before_type = before_type.replace(old, new)
        expected = '*struct{Data' + before_type.removeprefix('*') + '`json:"data"`}'
        if not before_type.startswith('*') or field_type(after, field) != expected:
            raise ValueError('reviewed payload changed beyond its data envelope: ' + name)
        text = text[:begin] + after + text[end:]
    for old, new in renames.items():
        text = re.sub(r'\b' + old + r'\b', new, text)
    return text, removed


if __name__ == '__main__':
    baseline, current = map(Path, sys.argv[1:])
    review = json.loads((ROOT/'deploy/contracts/sdk-v2-reviewed-breaks.json').read_text())
    if review['release'] != 'v2.0.0' or '\nversion: 2.0.0\n' not in (ROOT/'deploy/chart/Chart.yaml').read_text():
        raise ValueError('SDK break review applies only to v2.0.0')
    text, count = reviewed_baseline(baseline.read_bytes(), current.read_bytes(), review)
    baseline.write_text(text)
    print(f"SDK compatibility: {count} reviewed UI fields removed, {len(review.get('wrapped_fields', {}))} exact Charlie data envelopes, {len(review.get('renamed_types', {}))} type rename; all other public symbols checked")
