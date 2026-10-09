#!/usr/bin/env python3
import copy
import datetime as dt
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[2]
def module(name,path):
    spec=importlib.util.spec_from_file_location(name,ROOT/path); m=importlib.util.module_from_spec(spec); spec.loader.exec_module(m); return m
publication=module('publication','scripts/release-publication-policy.py')
runtime=module('runtime','scripts/qualify-release-runtime-images.py')
ruleset=module('ruleset','scripts/check-security-policy-contract.py')

class PolicyContractTest(unittest.TestCase):
    def test_future_deferrals_rejected(self):
        for tag in ('v1.2.2','v2.0.0'):
            with self.subTest(tag=tag),self.assertRaises(ValueError): publication.require_historical_automated(tag)
        publication.require_historical_automated('v1.2.1')
    def test_policy_no_new_approval(self):
        p=publication.load(); p['maintenance_automated_policy']['applicable_tags']=['v1.2.2']
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'policy.json'; path.write_text(json.dumps(p))
            with self.assertRaises(ValueError): publication.load(path)
    def test_future_exact_owned_waivers(self):
        now=dt.datetime(2026,10,9,tzinfo=dt.timezone.utc)
        w=dict(reference='registry.example/image@sha256:'+'a'*64,category='vulnerability',ids=['CVE-2026-1234'],reason='synthetic fixture',approved_by='fixture',expires_at='2026-10-10T00:00:00Z',release_version='v1.2.2')
        d={'schema_version':1,'waivers':[w]}
        with self.assertRaises(ValueError): runtime.waiver_map(d,now,'v1.2.2')
        w.update(owner='fixture',verification_evidence='fixture availability record',availability_checked_at='2026-10-09T00:00:00Z',upstream_fix_status='no_fixed_upstream_available')
        self.assertEqual(len(runtime.waiver_map(d,now,'v1.2.2')),1)
        for changes in ({'availability_checked_at':'2026-09-01T00:00:00Z'},{'upstream_fix_status':'fixed_compatible'},{'release_version':'v1.2.1'}):
            bad=copy.deepcopy(d);bad['waivers'][0].update(changes)
            with self.subTest(changes=changes),self.assertRaises(ValueError):runtime.waiver_map(bad,now,'v1.2.2')
    def test_workflow_report_enforcement_and_fork_contract(self):
        text=(ROOT/'.github/workflows/security-analysis.yaml').read_text()
        for contract in ('fetch-depth: 0','persist-credentials: false','--redact=100','--log-opts=--all','scripts/security-source-policy.py','if-no-files-found: error','github.event.pull_request.head.repo.full_name == github.repository','env -u GOSEC_AI_PROVIDER -u GOSEC_AI_API_KEY -u GOSEC_AI_BASE_URL','export GOWORK=off'):
            self.assertIn(contract,text)
        self.assertEqual(text.count('-no-fail'),1)
        self.assertLess(text.index('Collect source scans'),text.index('Enforce source security policy'))
        self.assertIn('if: ${{ always() }}',text[text.index('Retain sanitized'):])
        self.assertNotIn('path: security-raw',text)
    def test_ruleset_exact_emitted_checks(self):
        patch=json.loads((ROOT/'deploy/security/main-ruleset.patch.json').read_text())
        self.assertEqual(patch['bypass_actors'],[])
        self.assertEqual(patch['conditions']['ref_name']['include'],['refs/heads/main'])
        self.assertEqual({r['type'] for r in patch['rules']},{'deletion','non_fast_forward','pull_request','required_status_checks'})
        checks=next(r for r in patch['rules'] if r['type']=='required_status_checks')['parameters']['required_status_checks']
        workflows={'api contract gate':'api-contract','same-commit PR qualification aggregate':'pr-validation','source security policy gate':'security-analysis'}
        self.assertEqual({c['context'] for c in checks},set(workflows))
        for check in checks:
            self.assertEqual(check['integration_id'],15368)
            self.assertIn('name: '+check['context'],(ROOT/f'.github/workflows/{workflows[check["context"]]}.yaml').read_text())
    def test_ruleset_negative_contracts(self):
        patch=json.loads((ROOT/'deploy/security/main-ruleset.patch.json').read_text())
        self.assertTrue(ruleset.validate(patch))
        for field,value in [('bypass_actors',[{'actor_id':1,'actor_type':'Team','bypass_mode':'always'}]),('enforcement','evaluate'),('conditions',{'ref_name':{'include':['refs/heads/other'],'exclude':[]}})]:
            bad=copy.deepcopy(patch);bad[field]=value
            with self.subTest(field=field),self.assertRaises(ValueError): ruleset.validate(bad)
        bad=copy.deepcopy(patch)
        bad['rules'][-1]['parameters']['required_status_checks'][0]['context']='invented green check'
        with self.assertRaises(ValueError): ruleset.validate(bad)

    def test_reverted_synthetic_secret_full_history(self):
        binary=os.environ.get('GITLEAKS_BIN')
        if not binary: self.skipTest('set GITLEAKS_BIN for the required integration fixture')
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp)
            def git(*args): subprocess.run(['git',*args],cwd=root,check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            git('init');git('config','user.name','Synthetic Fixture');git('config','user.email','fixture@example.invalid')
            marker='ASTRONOMER_SYNTHETIC_NONSECRET_HISTORY_FIXTURE'
            (root/'fixture.txt').write_text(marker+'\n');git('add','.');git('commit','-m','synthetic nonsecret marker')
            (root/'fixture.txt').unlink();git('add','-u');git('commit','-m','remove marker')
            (root/'fixture.toml').write_text('title="synthetic nonsecret test"\n[[rules]]\nid="synthetic-history"\nregex="'+marker+'"\n')
            report=root/'report.json'
            result=subprocess.run([binary,'git','--config=fixture.toml','--redact=100','--no-banner','--log-opts=--all','--report-format=json','--report-path='+str(report),'.'],cwd=root,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            self.assertEqual(result.returncode,1)
            records=json.loads(report.read_text());self.assertTrue(records)
            self.assertTrue(all(x['RuleID']=='synthetic-history' for x in records))
            self.assertTrue(all(marker not in x.get('Secret','') for x in records))

if __name__=='__main__': unittest.main()
