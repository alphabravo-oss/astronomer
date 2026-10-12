#!/usr/bin/env python3
"""Fail closed if the temporary released-dependency scanner closure drifts."""
import datetime as dt
import hashlib
import json
from pathlib import Path
import re
ROOT=Path(__file__).resolve().parents[1]
KEYS={'schema_version','compiler','scanner_module','scanner_version','x_tools_version','go_mod_sha256','go_sum_sha256','review_until','purpose','exit_condition'}
def sha(data): return 'sha256:'+hashlib.sha256(data).hexdigest()
def validate(record, module, sums, now=None):
    now=now or dt.datetime.now(dt.timezone.utc)
    if set(record)!=KEYS or record['schema_version']!=1 or record['purpose']!='released_upstream_build_tool_dependency_compatibility':
        raise ValueError('scanner closure record invalid')
    if record['scanner_module']!='github.com/securego/gosec/v2' or not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+',record['scanner_version']) or not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+',record['x_tools_version']):
        raise ValueError('scanner must use released upstream module versions')
    if record['go_mod_sha256']!=sha(module) or record['go_sum_sha256']!=sha(sums):
        raise ValueError('scanner module/lock binding changed')
    text=module.decode()
    if re.search(r'^\s*(replace|exclude)\b',text,re.M) or 'tool github.com/securego/gosec/v2/cmd/gosec' not in text:
        raise ValueError('scanner source replacements/exclusions forbidden')
    for name,version in ((record['scanner_module'],record['scanner_version']),('golang.org/x/tools',record['x_tools_version'])):
        if not re.search(r'^\s*'+re.escape(name)+r'\s+'+re.escape(version)+r'(?:\s|$)',text,re.M):
            raise ValueError('scanner version disagrees with closure record')
    if not re.search(r'^go '+re.escape(record['compiler'])+r'$',text,re.M):
        raise ValueError('scanner compiler disagrees with record')
    deadline=dt.datetime.fromisoformat(record['review_until'].replace('Z','+00:00'))
    if deadline.tzinfo is None or deadline.utcoffset()!=dt.timedelta(0) or deadline<=now or not record['exit_condition'].strip():
        raise ValueError('scanner compatibility review expired or invalid')
    return True
if __name__=='__main__':
    directory=ROOT/'tools/gosec'
    if list(directory.rglob('*.go')): raise SystemExit('Copied scanner Go source is forbidden')
    validate(json.loads((directory/'closure.json').read_text()),(directory/'go.mod').read_bytes(),(directory/'go.sum').read_bytes())
    print('Scanner dependency closure: PASS (compatibility review; not source-security qualification)')
