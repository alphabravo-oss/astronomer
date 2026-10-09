#!/usr/bin/env python3
"""Check retained W02 evidence binding; unsigned records never approve a release."""
import hashlib
import json
from pathlib import Path
import subprocess
ROOT=Path(__file__).resolve().parents[4]
HERE=Path(__file__).resolve().parent
def digest(data):return 'sha256:'+hashlib.sha256(data).hexdigest()
def main():
    record=json.loads((HERE/'verification.json').read_text())
    if set(record)!={'schema_version','source_commit','observed_at','qualification','tasks','commands','artifacts'} or record['schema_version']!=1 or record['qualification']!='blocked_unaccepted_source_findings':
        raise ValueError('verification record schema/result invalid')
    for relative,expected in record['artifacts'].items():
        original=HERE/relative;resolved=original.resolve()
        if original.is_symlink() or not resolved.is_relative_to(HERE) or digest(resolved.read_bytes())!=expected:
            raise ValueError('artifact binding failed')
    compatibility=json.loads((HERE/'scanner-compatibility.json').read_text())
    if compatibility['source_commit']!=record['source_commit']:
        raise ValueError('source commit binding failed')
    for path,expected in compatibility['source_inputs'].items():
        data=subprocess.check_output(['git','show',record['source_commit']+':'+path],cwd=ROOT)
        if digest(data)!=expected:raise ValueError('source input binding failed')
    scanner=compatibility['astronomer_build_tool_closure']
    for name in ('go_mod','go_sum'):
        if digest((ROOT/'tools/gosec'/name.replace('_','.')).read_bytes())!=scanner[name+'_sha256']:
            raise ValueError('tool closure binding failed')
    findings=json.loads((HERE/'source-findings.json').read_text())['findings']
    if len(findings)!=575 or len({f['fingerprint'] for f in findings})!=575:
        raise ValueError('finding count/fingerprint identity mismatch')
    for finding in findings:
        data=subprocess.check_output(['git','show',record['source_commit']+':'+finding['path']],cwd=ROOT)
        if digest(data)!=finding['source_digest']:raise ValueError('finding source binding failed')
    for tool in ('gosec','govulncheck'):
        sarif=json.loads((HERE/(tool+'.sarif')).read_text())
        if len(sarif['runs'][0]['results'])!=sum(f['tool']==tool for f in findings):raise ValueError('SARIF retention count mismatch')
    print('W02 evidence binding: PASS (unsigned; source-security qualification remains blocked)')
if __name__=='__main__':main()
