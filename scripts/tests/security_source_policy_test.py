#!/usr/bin/env python3
import copy
import datetime as dt
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
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

if __name__=='__main__': unittest.main()
