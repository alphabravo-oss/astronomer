#!/usr/bin/env python3
"""Remove only reviewed v2 UI response fields from the v1 SDK compile fixture."""
import hashlib
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parent.parent


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
    return text, removed


if __name__ == '__main__':
    baseline, current = map(Path, sys.argv[1:])
    review = json.loads((ROOT/'deploy/contracts/sdk-v2-reviewed-breaks.json').read_text())
    if review['release'] != 'v2.0.0' or '\nversion: 2.0.0\n' not in (ROOT/'deploy/chart/Chart.yaml').read_text():
        raise ValueError('SDK break review applies only to v2.0.0')
    text, count = reviewed_baseline(baseline.read_bytes(), current.read_bytes(), review)
    baseline.write_text(text)
    print(f'SDK compatibility: {count} exact reviewed v2 tool response fields excluded; all other public symbols checked')
