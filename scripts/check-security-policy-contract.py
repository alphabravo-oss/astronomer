#!/usr/bin/env python3
"""Validate the proposed required-check identities against workflow job names."""
import json
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CHECKS={'api contract gate':'api-contract','same-commit PR qualification aggregate':'pr-validation','source security policy gate':'security-analysis'}
def validate(patch, root=ROOT):
    if set(patch)!={'name','target','enforcement','bypass_actors','conditions','rules'} or patch['target']!='branch' or patch['enforcement']!='active' or patch['bypass_actors']:
        raise ValueError('ruleset schema or bypass policy invalid')
    if patch['conditions']!={'ref_name':{'include':['refs/heads/main'],'exclude':[]}}:
        raise ValueError('ruleset must protect exact main branch')
    rules=patch['rules']
    if len(rules)!=4 or {r['type'] for r in rules}!={'deletion','non_fast_forward','pull_request','required_status_checks'}:
        raise ValueError('required rules missing or duplicated')
    for simple in ('deletion','non_fast_forward'):
        if next(r for r in rules if r['type']==simple)!={'type':simple}: raise ValueError('unexpected rule parameters')
    review=next(r for r in rules if r['type']=='pull_request')
    if review!={'type':'pull_request','parameters':{'required_approving_review_count':1,'dismiss_stale_reviews_on_push':True,'require_code_owner_review':False,'require_last_push_approval':True,'required_review_thread_resolution':True}}:
        raise ValueError('review policy invalid')
    status=next(r for r in rules if r['type']=='required_status_checks')
    if set(status)!={'type','parameters'} or set(status['parameters'])!={'strict_required_status_checks_policy','required_status_checks'} or status['parameters']['strict_required_status_checks_policy'] is not True:
        raise ValueError('status check policy invalid')
    checks=status['parameters']['required_status_checks']
    if len(checks)!=len(CHECKS) or {c['context'] for c in checks}!=set(CHECKS): raise ValueError('required check identities invalid')
    for check in checks:
        if set(check)!={'context','integration_id'} or check['integration_id']!=15368: raise ValueError('check producer must be GitHub Actions')
        text=(root/f'.github/workflows/{CHECKS[check["context"]]}.yaml').read_text()
        if 'name: '+check['context'] not in text: raise ValueError('required check not emitted by workflow')
    return True
if __name__=='__main__':
    validate(json.loads((ROOT/'deploy/security/main-ruleset.patch.json').read_text()))
    print('Proposed main ruleset contract: PASS (external settings not applied)')
