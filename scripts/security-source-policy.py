#!/usr/bin/env python3
"""Enforce source findings after collection; reports never contain source snippets."""
import argparse
import datetime as dt
import hashlib
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
EXCEPTION_KEYS = {'tool','rule_id','path','line','column','source_digest','fingerprint','release_scope','reason','owner','approved_by','evidence','expires_at'}

def digest(data):
    return 'sha256:' + hashlib.sha256(data).hexdigest()

def normalize(tool, rule, path, line, level, root, column=1):
    if not re.fullmatch(r'G[0-9]{3}' if tool == 'gosec' else r'GO-[0-9]{4}-[0-9]{4,}', rule):
        raise ValueError('unknown finding identifier')
    original = Path(path)
    target = original if original.is_absolute() else root / original
    resolved = target.resolve()
    if target.is_symlink() or not resolved.is_relative_to(root) or not resolved.is_file():
        raise ValueError('finding path is not a source file')
    if not isinstance(line, int) or isinstance(line, bool) or line < 1:
        raise ValueError('finding line is invalid')
    if type(column) is not int or column < 1:
        raise ValueError('finding column invalid')
    item = dict(tool=tool, rule_id=rule, path=resolved.relative_to(root).as_posix(), line=line, column=column,
                source_digest=digest(resolved.read_bytes()), level=level)
    item['fingerprint'] = digest(json.dumps(item, sort_keys=True, separators=(',', ':')).encode())
    return item

def evaluate(gosec, vuln, exceptions, statuses, scope, root=ROOT, now=None):
    root = root.resolve()
    now = now or dt.datetime.now(dt.timezone.utc)
    failures, findings = [], []
    if set(statuses) != {'gosec','govulncheck'} or any(type(v) is not int for v in statuses.values()):
        raise ValueError('scanner status document is invalid')
    if any(statuses.values()):
        failures.append('scanner execution failed')
    if not isinstance(gosec.get('Issues'), list) or not isinstance(gosec.get('Golang errors'), dict) or gosec.get('Stats', {}).get('files', 0) < 1:
        failures.append('gosec report is missing or malformed')
        gosec = {'Issues':[], 'Golang errors':{}}
    if gosec['Golang errors']:
        failures.append('gosec analysis incomplete')
    for issue in gosec['Issues']:
        severity = issue['severity']
        if severity not in {'HIGH','MEDIUM','LOW'}:
            raise ValueError('gosec severity is invalid')
        findings.append(normalize('gosec', issue['rule_id'], issue['file'], int(str(issue['line']).split('-')[0]), severity, root, int(issue.get('column', 1))))
    if vuln.get('version') != '2.1.0' or not isinstance(vuln.get('runs'), list) or len(vuln['runs']) != 1:
        failures.append('govulncheck SARIF is missing or malformed')
        vuln = {'runs':[{'tool':{'driver':{'name':'govulncheck'}},'results':[]}]}
    run = vuln['runs'][0]
    if run.get('tool', {}).get('driver', {}).get('name') != 'govulncheck' or not isinstance(run.get('results', []), list):
        failures.append('govulncheck SARIF tool/results invalid')
        run = {'results':[]}
    for result in run.get('results', []):
        level = result['level']
        if level not in {'error','warning','note'}:
            raise ValueError('govulncheck reachability level is invalid')
        # Symbol-scan SARIF attaches findings to go.mod; error means called symbol.
        findings.append(normalize('govulncheck', result['ruleId'], 'go.mod', 1, level, root))
    findings = list({f['fingerprint']: f for f in findings}.values())
    if set(exceptions) != {'schema_version','exceptions'} or exceptions['schema_version'] != 1 or not isinstance(exceptions['exceptions'], list):
        raise ValueError('exception document violates closed schema')
    by_fingerprint = {f['fingerprint']: f for f in findings}
    accepted = set()
    for exc in exceptions['exceptions']:
        if not isinstance(exc, dict) or set(exc) != EXCEPTION_KEYS:
            raise ValueError('exception violates closed schema')
        if any(not isinstance(exc[k], str) or not exc[k].strip() for k in EXCEPTION_KEYS - {'line','column'}):
            raise ValueError('exception metadata missing')
        if type(exc['line']) is not int or exc['line'] < 1 or type(exc['column']) is not int or exc['column'] < 1:
            raise ValueError('exception line invalid')
        expiry = dt.datetime.fromisoformat(exc['expires_at'].replace('Z', '+00:00'))
        if expiry.tzinfo is None or expiry.utcoffset() != dt.timedelta(0) or expiry <= now:
            raise ValueError('exception expired or not UTC')
        if exc['release_scope'] != scope:
            raise ValueError('exception scope does not match')
        fingerprint = exc['fingerprint']
        if fingerprint in accepted:
            raise ValueError('duplicate exception')
        finding = by_fingerprint.get(fingerprint)
        if finding is None or any(exc[k] != finding[k] for k in ('tool','rule_id','path','line','column','source_digest')):
            raise ValueError('exception is unknown, unmatched, or source changed')
        accepted.add(fingerprint)
    policy = json.loads((root / 'deploy/security/source-policy.json').read_text())
    required_policy = {'schema_version','gosec_blocked_severities','gosec_medium_policy','govulncheck_scan_level','govulncheck_blocked_level','triage_owner','triage_cadence','recommended_fix_days','no_fix_policy'}
    if not isinstance(policy, dict) or set(policy) != required_policy or type(policy['schema_version']) is not int or policy['schema_version'] != 1:
        raise ValueError('source policy violates closed schema')
    days = policy['recommended_fix_days']
    if not isinstance(days, dict) or set(days) != {'critical_or_exploited','high'} or any(type(value) is not int or value < 1 for value in days.values()):
        raise ValueError('source response targets invalid')
    if not isinstance(policy['triage_owner'], str) or not policy['triage_owner'].strip() or policy['triage_cadence'] != 'daily' or policy['no_fix_policy'] != 'exact_scoped_expiring_exception_with_findings_retained':
        raise ValueError('source ownership or no-fix policy invalid')
    if policy['gosec_blocked_severities'] != ['HIGH'] or policy['govulncheck_scan_level'] != 'symbol' or policy['govulncheck_blocked_level'] != 'error' or policy['gosec_medium_policy'] != 'retain_and_require_owned_review':
        raise ValueError('unsupported source policy')
    reviews_path = root / 'deploy/security/source-reviews.json'
    reviews = json.loads(reviews_path.read_text())
    if set(reviews) != {'schema_version','reviews'} or reviews['schema_version'] != 1 or not isinstance(reviews['reviews'], list):
        raise ValueError('owned review document invalid')
    owned = {}
    for review in reviews['reviews']:
        if set(review) != {'fingerprint','owner','review_by','status','evidence'} or review['status'] not in {'pending','reviewed','blocked'} or not review['owner'].strip():
            raise ValueError('owned review metadata invalid')
        if review['fingerprint'] in owned:
            raise ValueError('duplicate owned review')
        deadline = dt.datetime.fromisoformat(review['review_by'].replace('Z','+00:00'))
        if deadline.tzinfo is None or deadline.utcoffset() != dt.timedelta(0):
            raise ValueError('owned review deadline must be UTC')
        if review['status'] in {'reviewed','blocked'} and not review['evidence'].strip():
            raise ValueError('completed review requires evidence')
        owned[review['fingerprint']] = (review, deadline)
    for finding in findings:
        finding['accepted'] = finding['fingerprint'] in accepted
        if not finding['accepted'] and ((finding['tool'] == 'gosec' and finding['level'] == 'HIGH') or (finding['tool'] == 'govulncheck' and finding['level'] == 'error')):
            failures.append('unaccepted ' + finding['tool'] + ' finding')
        if finding['tool'] == 'gosec' and finding['level'] == 'MEDIUM':
            review_entry = owned.get(finding['fingerprint'])
            if review_entry is None:
                failures.append('MED finding has no exact owned review')
                finding['review_status'] = 'unassigned'
            else:
                review, deadline = review_entry
                finding['review_owner'] = review['owner']
                finding['review_due_at'] = review['review_by']
                finding['review_status'] = review['status']
                if review['status'] == 'blocked':
                    failures.append('MED finding has an unresolved confirmed defect')
                if review['status'] == 'pending' and deadline <= now:
                    failures.append('MED finding review is overdue')
    return dict(schema_version=1, release_scope=scope, passed=not failures, failures=sorted(set(failures)), findings=findings)

def sarif(summary, tool):
    findings = [f for f in summary['findings'] if f['tool'] == tool]
    results = []
    for f in findings:
        level = f['level'] if tool == 'govulncheck' else {'HIGH':'error','MEDIUM':'warning','LOW':'note'}[f['level']]
        results.append(dict(ruleId=f['rule_id'], level=level, message={'text':'Retained source security finding; consult policy summary for acceptance and reachability.'},
                            locations=[{'physicalLocation':{'artifactLocation':{'uri':f['path']},'region':{'startLine':f['line'],'startColumn':f['column']}}}],
                            partialFingerprints={'sourcePolicy/v1':f['fingerprint']}, properties={'policyAccepted':f['accepted']}))
    return {'version':'2.1.0','$schema':'https://json.schemastore.org/sarif-2.1.0.json','runs':[{'tool':{'driver':{'name':tool,'rules':[]}},'results':results}]}

def main():
    parser = argparse.ArgumentParser()
    for name in ('gosec','govulncheck','statuses','exceptions','output'):
        parser.add_argument('--'+name, type=Path, required=True)
    parser.add_argument('--scope', required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    def read_report(path):
        try: return json.loads(path.read_text())
        except (ValueError, OSError): return {}
    try:
        summary = evaluate(read_report(args.gosec), read_report(args.govulncheck),
                           json.loads(args.exceptions.read_text()), json.loads(args.statuses.read_text()), args.scope)
    except (ValueError, KeyError, TypeError, AttributeError, OSError, json.JSONDecodeError):
        # Never echo malformed report/exception content or scanner source snippets.
        try:
            summary = evaluate(read_report(args.gosec), read_report(args.govulncheck),
                               {'schema_version':1,'exceptions':[]}, json.loads(args.statuses.read_text()), args.scope)
            summary['passed'] = False
            summary['failures'].append('invalid or unavailable exception/security evidence')
        except (ValueError, KeyError, TypeError, AttributeError, OSError):
            summary = dict(schema_version=1, release_scope=args.scope, passed=False, failures=['invalid or unavailable security evidence'], findings=[])
    (args.output/'summary.json').write_text(json.dumps(summary, indent=2)+'\n')
    for tool in ('gosec','govulncheck'):
        (args.output/(tool+'.sarif')).write_text(json.dumps(sarif(summary, tool), indent=2)+'\n')
    print('Source security policy: '+('PASS' if summary['passed'] else 'FAIL')+'; retained findings: '+str(len(summary['findings'])))
    return 0 if summary['passed'] else 1

if __name__ == '__main__':
    raise SystemExit(main())
