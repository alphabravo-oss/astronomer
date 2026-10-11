#!/usr/bin/env python3
import copy
import gzip
import datetime as dt
import importlib.util
import json
import os
import subprocess
import hashlib
from pathlib import Path
import tempfile
import unittest

ROOT = Path(os.environ.get('ASTRONOMER_POLICY_REPO', Path(__file__).resolve().parents[2])).resolve()
EXCEPTIONS = Path(os.environ.get('PROPOSED_SOURCE_EXCEPTIONS', ROOT/'deploy/security/source-exceptions.json'))
spec = importlib.util.spec_from_file_location('policy', ROOT/'scripts/security-source-policy.py')
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)

class SecurityPolicyTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root/'deploy/security').mkdir(parents=True)
        (self.root/'deploy/security/source-policy.json').write_bytes((ROOT/'deploy/security/source-policy.json').read_bytes())
        (self.root/'deploy/security/source-reviews.json').write_text('{"schema_version":1,"reviews":[]}')
        (self.root/'go.mod').write_text('module synthetic\n')
        (self.root/'sample.go').write_text('package synthetic\n')
        self.g = {'Issues':[], 'Golang errors':{}, 'Stats':{'files':1}}
        self.v = {'version':'2.1.0','runs':[{'tool':{'driver':{'name':'govulncheck'}},'results':[]}]}
        self.e = {'schema_version':1,'exceptions':[]}
        self.status = {'gosec':0,'govulncheck':0}
        self.now = dt.datetime(2026,10,9,tzinfo=dt.timezone.utc)
    def run_policy(self):
        return policy.evaluate(self.g,self.v,self.e,self.status,'v1.2.2',self.root,self.now)
    def high(self):
        self.g['Issues'].append({'severity':'HIGH','rule_id':'G101','file':'sample.go','line':'1','code':'DO NOT RETAIN SYNTHETIC SOURCE SNIPPET'})
    def exception(self):
        f=self.run_policy()['findings'][0]
        self.e['exceptions']=[{k:f[k] for k in ('tool','rule_id','path','line','column','source_digest','fingerprint')} | {'release_scope':'v1.2.2','reason':'synthetic fixture only','owner':'test','approved_by':'fixture','evidence':'fixture','expires_at':'2026-10-10T00:00:00Z'}]
    def test_clean(self):
        self.assertTrue(self.run_policy()['passed'])
    def test_high_blocks_and_retains(self):
        self.high(); result=self.run_policy()
        self.assertFalse(result['passed']); self.assertEqual(len(result['findings']),1)
        self.assertNotIn('SOURCE SNIPPET', json.dumps(result))
    def test_exact_exception_passes_and_retains(self):
        self.high(); self.exception(); r=self.run_policy()
        self.assertTrue(r['passed']); self.assertTrue(r['findings'][0]['accepted'])
    def test_new_column_same_rule_line_blocks(self):
        self.high(); self.exception()
        self.g['Issues'].append({'severity':'HIGH','rule_id':'G101','file':'sample.go','line':'1','column':'2'})
        self.assertFalse(self.run_policy()['passed'])
        self.assertEqual(len(self.run_policy()['findings']),2)
    def test_changed_source_rejected(self):
        self.high(); self.exception(); (self.root/'sample.go').write_text('package changed\n')
        with self.assertRaises(ValueError): self.run_policy()
    def test_new_finding_blocks(self):
        self.high(); self.exception(); self.g['Issues'].append({'severity':'HIGH','rule_id':'G404','file':'sample.go','line':'1'})
        self.assertFalse(self.run_policy()['passed'])
    def test_expired_scope_unknown_duplicate_unmatched_closed(self):
        self.high(); self.exception(); base=copy.deepcopy(self.e)
        changes=[{'expires_at':'2026-10-08T00:00:00Z'},{'release_scope':'v1.2.1'},{'rule_id':'G999'},{'fingerprint':'sha256:'+'0'*64},{'owner':''},{'wildcard':'*'}]
        for change in changes:
            self.e=copy.deepcopy(base); self.e['exceptions'][0].update(change)
            with self.subTest(change=change),self.assertRaises(ValueError): self.run_policy()
        self.e=copy.deepcopy(base); self.e['exceptions']*=2
        with self.assertRaises(ValueError): self.run_policy()
    def test_scanner_errors_block(self):
        self.status['gosec']=1; self.assertFalse(self.run_policy()['passed'])
        self.status['gosec']=0; self.g['Golang errors']={'sample.go':[{'error':'synthetic'}]}
        self.assertFalse(self.run_policy()['passed'])
    def test_missing_malformed_reports(self):
        self.g['Stats']['files']=0
        self.assertFalse(self.run_policy()['passed'])
        self.g['Stats']['files']=1; self.v['runs']=[]
        self.assertFalse(self.run_policy()['passed'])
    def test_reachability_not_severity(self):
        for level in ('error','warning','note'):
            self.v['runs'][0]['results']=[{'ruleId':'GO-2026-6617','level':level}]
            r=self.run_policy(); self.assertEqual(r['passed'],level!='error')
            self.assertEqual(r['findings'][0]['level'],level)
            self.assertNotIn('severity',r['findings'][0])
    def test_medium_owned_review(self):
        self.g['Issues']=[{'severity':'MEDIUM','rule_id':'G304','file':'sample.go','line':'1'}]
        self.assertFalse(self.run_policy()['passed'])
        finding=self.run_policy()['findings'][0]
        review={'fingerprint':finding['fingerprint'],'owner':'security/platform','review_by':'2026-10-16T00:00:00Z','status':'pending','evidence':''}
        path=self.root/'deploy/security/source-reviews.json'
        path.write_text(json.dumps({'schema_version':1,'reviews':[review]}))
        r=self.run_policy(); self.assertTrue(r['passed']); self.assertEqual(r['findings'][0]['review_owner'],'security/platform')
        review['review_by']='2026-10-08T00:00:00Z'
        path.write_text(json.dumps({'schema_version':1,'reviews':[review]}))
        self.assertFalse(self.run_policy()['passed'])
    def test_confirmed_medium_defect_blocks_even_before_review_deadline(self):
        self.g['Issues']=[{'severity':'MEDIUM','rule_id':'G705','file':'sample.go','line':'1'}]
        finding=self.run_policy()['findings'][0]
        review={'fingerprint':finding['fingerprint'],'owner':'security/platform','review_by':'2026-10-16T00:00:00Z','status':'blocked','evidence':'Confirmed source-to-sink defect; repair required.'}
        path=self.root/'deploy/security/source-reviews.json'
        path.write_text(json.dumps({'schema_version':1,'reviews':[review]}))
        result=self.run_policy()
        self.assertFalse(result['passed'])
        self.assertIn('MED finding has an unresolved confirmed defect', result['failures'])
        self.assertEqual(result['findings'][0]['review_status'], 'blocked')
        review['status']='reviewed'
        path.write_text(json.dumps({'schema_version':1,'reviews':[review]}))
        self.assertTrue(self.run_policy()['passed'])

    def test_symlink_outside_and_unknown_rule(self):
        self.high(); (self.root/'sample.go').unlink(); (self.root/'sample.go').symlink_to(ROOT/'go.mod')
        with self.assertRaises(ValueError): self.run_policy()
    def test_closed_source_policy(self):
        path=self.root/'deploy/security/source-policy.json';base=json.loads(path.read_text())
        for update in ({'unknown':True},{'triage_owner':''},{'recommended_fix_days':{'high':True,'critical_or_exploited':3}},{'schema_version':True},{'triage_cadence':'never'}):
            doc=copy.deepcopy(base);doc.update(update);path.write_text(json.dumps(doc))
            with self.subTest(update=update),self.assertRaises(ValueError):self.run_policy()
        doc=copy.deepcopy(base);del doc['no_fix_policy'];path.write_text(json.dumps(doc))
        with self.assertRaises(ValueError):self.run_policy()
    def test_sarif_retained_on_failure(self):
        self.high(); r=self.run_policy(); s=policy.sarif(r,'gosec')
        self.assertEqual(len(s['runs'][0]['results']),1); self.assertNotIn('SOURCE SNIPPET',json.dumps(s))

class ReviewedSourceControlsTest(unittest.TestCase):
    """Exercise immutable historical reviews without granting namespaces or rules."""

    def setUp(self):
        SecurityPolicyTest.setUp(self)
        evidence = ROOT/'docs/assurance/technology-guide/w02/current-source-927151be'
        bindings = json.loads((evidence/'sql-control-current-binding-proposal.json').read_text())
        nonce = json.loads((evidence/'fips-random-nonce-control-proposal.json').read_text())
        self.controls = [row['current_finding'] for row in bindings['checks']] + [nonce['finding']]
        self.assertEqual(len(self.controls), 63)
        self.assertEqual(len({row['fingerprint'] for row in self.controls}), 63)
        for finding in self.controls:
            path = self.root/finding['path']
            path.parent.mkdir(parents=True, exist_ok=True)
            original = immutable_git_blob('927151bebf20c6a8f63fe76ff804b45e86044ded', finding['path'])
            self.assertEqual(policy.digest(original), finding['source_digest'])
            path.write_bytes(original)
            self.g['Issues'].append({
                'severity': finding['level'], 'rule_id': finding['rule_id'],
                'file': finding['path'], 'line': str(finding['line']),
                'column': str(finding['column']),
            })
        self.e = json.loads(immutable_git_blob('5d56e7ba9df17115a2650353b368c92de4f2bd4d', 'deploy/security/source-exceptions.json'))
        known = {row['fingerprint'] for row in self.controls}
        self.e['exceptions'] = [row for row in self.e['exceptions'] if row['fingerprint'] in known]
        # Production uses the real clock. This fixture evaluates the dated reviews
        # within their explicit lifetime, then tests expiry separately.
        self.now = dt.datetime(2026, 10, 10, tzinfo=dt.timezone.utc)

    def run_policy(self):
        return policy.evaluate(self.g, self.v, self.e, self.status,
                               'first-party-source-policy', self.root, self.now)

    def test_all_63_controls_are_exact_and_retained(self):
        result = self.run_policy()
        self.assertTrue(result['passed'])
        self.assertEqual(len(result['findings']), 63)
        self.assertTrue(all(row['accepted'] for row in result['findings']))

    def test_new_credential_in_same_generated_file_is_blocked(self):
        finding = self.controls[0]
        self.g['Issues'].append({
            'severity': 'HIGH', 'rule_id': 'G101', 'file': finding['path'],
            'line': str(finding['line']), 'column': str(finding['column'] + 1),
            'code': 'synthetic password value must not enter retained output',
        })
        result = self.run_policy()
        self.assertFalse(result['passed'])
        self.assertEqual(sum(row['accepted'] for row in result['findings']), 63)
        self.assertNotIn('synthetic password', json.dumps(result))

    def test_new_generated_file_is_blocked(self):
        path = self.root/'internal/db/sqlc/new_credentials.sql.go'
        path.write_text('package sqlc\nconst password = "synthetic fixture"\n')
        self.g['Issues'].append({'severity': 'HIGH', 'rule_id': 'G101',
                                 'file': str(path), 'line': '2', 'column': '7'})
        self.assertFalse(self.run_policy()['passed'])

    def test_changed_sql_or_nonce_source_invalidates_control(self):
        for finding in (self.controls[0], self.controls[-1]):
            path = self.root/finding['path']
            original = path.read_bytes()
            path.write_bytes(original + b'\n// changed security-sensitive source\n')
            with self.subTest(path=finding['path']), self.assertRaises(ValueError):
                self.run_policy()
            path.write_bytes(original)

    def test_new_fixed_nonce_site_is_blocked(self):
        path = self.root/'scripts/fips-mode-probe/other.go'
        path.write_text('package main\n// synthetic fixed external nonce fixture\n')
        self.g['Issues'].append({'severity': 'HIGH', 'rule_id': 'G407',
                                 'file': str(path), 'line': '2', 'column': '1'})
        self.assertFalse(self.run_policy()['passed'])

    def test_review_expiry_and_scope_fail_closed(self):
        self.now = dt.datetime(2026, 10, 16, tzinfo=dt.timezone.utc)
        with self.assertRaises(ValueError):
            self.run_policy()
        self.now = dt.datetime(2026, 10, 10, tzinfo=dt.timezone.utc)
        with self.assertRaises(ValueError):
            policy.evaluate(self.g, self.v, self.e, self.status,
                            'unreviewed-release', self.root, self.now)


def immutable_git_blob(commit, relative):
    """Read digest-bound historical fixtures without depending on local Git refs."""
    directory = ROOT/'scripts/testdata/security-source-policy'
    index = json.loads((directory/'index.json').read_text())
    digest = index[commit + ':' + relative]
    if len(digest) != 64 or any(char not in '0123456789abcdef' for char in digest):
        raise ValueError('invalid historical fixture digest')
    with gzip.open(directory/(digest + '.gz'), 'rb') as handle:
        data = handle.read(1024 * 1024 + 1)
    if not 0 < len(data) <= 1024 * 1024 or hashlib.sha256(data).hexdigest() != digest:
        raise ValueError('historical fixture digest or size drift')
    return data


def historical_audit_control():
    document = json.loads(immutable_git_blob(
        '49fb11df0ed3a7c9214caf2b2d41ce2889ebdb71', 'deploy/security/source-exceptions.json'))
    controls = [row for row in document['exceptions'] if row['rule_id'] == 'G109']
    if len(controls) != 1:
        raise ValueError('historical audit control inventory drift')
    return controls[0]


class ReviewedAuditStatusControlTest(unittest.TestCase):
    """Positive evidence belongs to the immutable 927 native source, not today."""
    def setUp(self):
        SecurityPolicyTest.setUp(self)
        findings = json.loads((ROOT/'docs/assurance/technology-guide/w02/current-source-927151be/source-findings.json').read_text())['findings']
        self.finding = next(row for row in findings if row['rule_id'] == 'G109')
        target = self.root/self.finding['path']
        target.parent.mkdir(parents=True, exist_ok=True)
        original = immutable_git_blob('927151bebf20c6a8f63fe76ff804b45e86044ded', self.finding['path'])
        self.assertEqual('sha256:' + hashlib.sha256(original).hexdigest(), self.finding['source_digest'])
        target.write_bytes(original)
        self.g['Issues'] = [{
            'severity': self.finding['level'], 'rule_id': 'G109',
            'file': self.finding['path'], 'line': str(self.finding['line']),
            'column': str(self.finding['column']),
        }]
        self.e = {'schema_version': 1, 'exceptions': [historical_audit_control()]}
        self.now = dt.datetime(2026, 10, 10, tzinfo=dt.timezone.utc)

    def run_policy(self):
        return policy.evaluate(self.g, self.v, self.e, self.status,
                               'first-party-source-policy', self.root, self.now)

    def test_exact_bounded_status_filter_retains_finding(self):
        result = self.run_policy()
        self.assertTrue(result['passed'])
        self.assertEqual(len(result['findings']), 1)
        self.assertTrue(result['findings'][0]['accepted'])
        self.assertEqual(result['findings'][0]['fingerprint'], self.finding['fingerprint'])

    def test_different_narrowing_in_same_file_is_blocked(self):
        self.g['Issues'].append(dict(self.g['Issues'][0], column=str(self.finding['column'] + 1)))
        result = self.run_policy()
        self.assertFalse(result['passed'])
        self.assertEqual(sum(row['accepted'] for row in result['findings']), 1)

    def test_changed_guard_invalidates_control(self):
        target = self.root/self.finding['path']
        body = target.read_text()
        self.assertIn('statusCode > 599', body)
        target.write_text(body.replace('statusCode > 599', 'statusCode > 999999999999'))
        with self.assertRaises(ValueError):
            self.run_policy()

    def test_historical_control_rejects_changed_current_source(self):
        (self.root/self.finding['path']).write_bytes((ROOT/self.finding['path']).read_bytes())
        with self.assertRaises(ValueError):
            self.run_policy()


class ReviewedCurrentAuditStatusControlTest(ReviewedAuditStatusControlTest):
    """Current static identity is separate from a fresh native collection."""
    def setUp(self):
        SecurityPolicyTest.setUp(self)
        review_bytes = (ROOT/'advisor-plans/032-evidence/audit-status-current-static-binding.json').read_bytes()
        self.assertEqual(hashlib.sha256(review_bytes).hexdigest(),
                         '790895ea8beb5f8116678feda457e2751f14bf398dbb3afc0dbeb6fd37c3554e')
        self.finding = json.loads(review_bytes)['finding']
        target = self.root/self.finding['path']
        target.parent.mkdir(parents=True, exist_ok=True)
        current = (ROOT/self.finding['path']).read_bytes()
        self.assertEqual('sha256:' + hashlib.sha256(current).hexdigest(), self.finding['source_digest'])
        target.write_bytes(current)
        self.g['Issues'] = [{'severity': self.finding['level'], 'rule_id': 'G109',
                             'file': self.finding['path'], 'line': str(self.finding['line']),
                             'column': str(self.finding['column'])}]
        controls = json.loads(EXCEPTIONS.read_text())['exceptions']
        self.e = {'schema_version': 1, 'exceptions': [
            row for row in controls if row['fingerprint'] == self.finding['fingerprint']]}
        self.assertEqual(len(self.e['exceptions']), 1)
        self.now = dt.datetime(2026, 10, 10, tzinfo=dt.timezone.utc)

    def test_historical_control_rejects_changed_current_source(self):
        self.e['exceptions'] = [historical_audit_control()]
        with self.assertRaises(ValueError):
            self.run_policy()

    def test_expiry_and_scope_fail_closed(self):
        self.now = dt.datetime(2026, 10, 16, tzinfo=dt.timezone.utc)
        with self.assertRaises(ValueError):
            self.run_policy()
        self.now = dt.datetime(2026, 10, 10, tzinfo=dt.timezone.utc)
        with self.assertRaises(ValueError):
            policy.evaluate(self.g, self.v, self.e, self.status, 'release-tag', self.root, self.now)




class HistoricalReviewed25SourceControlsTest(unittest.TestCase):
    """The historical 21 guards apply only to their immutable source snapshot."""
    def setUp(self):
        SecurityPolicyTest.setUp(self)
        directory = ROOT/'docs/assurance/technology-guide/w02/current-source-5c4d273f-reviewed-25'
        self.records = json.loads((directory/'reviewed-dispositions.json').read_text())['records']
        self.raw_rows = json.loads((directory/'raw-selected-findings.json').read_text())['rows']
        self.assertEqual(len(self.records), 21)
        known = {row['canonical_finding']['fingerprint'] for row in self.records}
        for row in self.raw_rows:
            finding = row['canonical_finding']
            target = self.root/finding['path']
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(immutable_git_blob('5c4d273ff564aab0ede8bb9265c789380f6f640d', finding['path']))
            self.assertEqual(policy.digest(target.read_bytes()), finding['source_digest'])
            if row['verdict'] == 'not_actionable':
                self.g['Issues'].append(copy.deepcopy(row['raw_issue']) | {'file':finding['path']})
        self.e = json.loads(immutable_git_blob('5d56e7ba9df17115a2650353b368c92de4f2bd4d', 'deploy/security/source-exceptions.json'))
        self.e['exceptions'] = [row for row in self.e['exceptions'] if row['fingerprint'] in known]
        self.assertEqual(len(self.e['exceptions']), 21)
        self.now = dt.datetime(2026,10,10,tzinfo=dt.timezone.utc)
    def run_policy(self):
        return policy.evaluate(self.g,self.v,self.e,self.status,'first-party-source-policy',self.root,self.now)
    def test_exact_current_21_retained_with_no_source_snippets(self):
        result = self.run_policy()
        self.assertTrue(result['passed']); self.assertEqual(len(result['findings']),21)
        self.assertTrue(all(row['accepted'] for row in result['findings']))
        self.assertTrue(all('code' not in row for row in result['findings']))
    def test_four_unresolved_findings_are_not_accepted(self):
        open_rows = [row for row in self.raw_rows if row['verdict']=='needs_review']
        self.assertEqual(len(open_rows),4)
        for row in open_rows:
            self.g['Issues'].append(copy.deepcopy(row['raw_issue']) | {'file':row['canonical_finding']['path']})
        result=self.run_policy();self.assertFalse(result['passed'])
        self.assertEqual(sum(row['accepted'] for row in result['findings']),21)
        self.assertEqual(sum(not row['accepted'] for row in result['findings']),4)
    def test_changed_source_and_new_column_do_not_inherit_guard(self):
        finding=self.records[0]['canonical_finding'];path=self.root/finding['path'];original=path.read_bytes()
        path.write_bytes(original+b'\n// security-sensitive change\n')
        with self.assertRaises(ValueError):self.run_policy()
        path.write_bytes(original)
        self.g['Issues'].append({'severity':'HIGH','rule_id':finding['rule_id'],'file':finding['path'],'line':str(finding['line']),'column':str(finding['column']+100)})
        result=self.run_policy();self.assertFalse(result['passed']);self.assertEqual(sum(row['accepted'] for row in result['findings']),21)
    def test_expiry_scope_and_scanner_incompleteness_fail(self):
        self.now=dt.datetime(2026,10,16,tzinfo=dt.timezone.utc)
        with self.assertRaises(ValueError):self.run_policy()
        self.now=dt.datetime(2026,10,10,tzinfo=dt.timezone.utc)
        with self.assertRaises(ValueError):policy.evaluate(self.g,self.v,self.e,self.status,'different-scope',self.root,self.now)
        self.g['Golang errors']={'sample.go':[{'error':'synthetic incomplete analysis'}]}
        self.assertFalse(self.run_policy()['passed'])

if __name__=='__main__': unittest.main()
