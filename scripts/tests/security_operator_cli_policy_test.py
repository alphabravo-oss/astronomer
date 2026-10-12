#!/usr/bin/env python3
"""Exercise ten reviewed identities; no scanner or current-source gate claim."""
import copy
import datetime as dt
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

ROOT = Path(os.environ.get('ASTRONOMER_POLICY_REPO', Path(__file__).resolve().parents[2])).resolve()
spec = importlib.util.spec_from_file_location('source_policy', ROOT / 'scripts/security-source-policy.py')
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)
REVIEW = 'advisor-plans/032-evidence/operator-cli-path-static-review.json'
REVIEW_SHA = '9353848458e9b9ba10b8a8eb5b76a77e15367a5cd1a66322057cd1dd121ce768'


class ReviewedOperatorCLIControlTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'deploy/security').mkdir(parents=True)
        (self.root / 'deploy/security/source-policy.json').write_bytes(
            (ROOT / 'deploy/security/source-policy.json').read_bytes())
        (self.root / 'deploy/security/source-reviews.json').write_text(
            '{"schema_version":1,"reviews":[]}')
        (self.root / 'go.mod').write_text('module synthetic\n')
        review_bytes = (ROOT / REVIEW).read_bytes()
        self.assertEqual(hashlib.sha256(review_bytes).hexdigest(), REVIEW_SHA)
        review = json.loads(review_bytes)
        self.controls = [row['finding'] for row in review['checks']]
        self.assertEqual(len(self.controls), 10)
        self.assertEqual(len({row['fingerprint'] for row in self.controls}), 10)
        self.g = {'Issues': [], 'Golang errors': {}, 'Stats': {'files': 5}}
        for finding in self.controls:
            source = (ROOT / finding['path']).read_bytes()
            self.assertEqual('sha256:' + hashlib.sha256(source).hexdigest(), finding['source_digest'])
            path = self.root / finding['path']
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(source)
            self.g['Issues'].append({
                'severity': finding['level'], 'rule_id': finding['rule_id'],
                'file': finding['path'], 'line': str(finding['line']),
                'column': str(finding['column']),
            })
        path = Path(os.environ.get('PROPOSED_SOURCE_EXCEPTIONS', ROOT / 'deploy/security/source-exceptions.json'))
        document = json.loads(path.read_text())
        known = {finding['fingerprint'] for finding in self.controls}
        self.e = {'schema_version': 1, 'exceptions': [
            row for row in document['exceptions'] if row['fingerprint'] in known]}
        self.v = {'version': '2.1.0', 'runs': [
            {'tool': {'driver': {'name': 'govulncheck'}}, 'results': []}]}
        self.status = {'gosec': 0, 'govulncheck': 0}
        self.now = dt.datetime(2026, 10, 10, tzinfo=dt.timezone.utc)

    def evaluate(self, scope='first-party-source-policy'):
        return policy.evaluate(self.g, self.v, self.e, self.status, scope, self.root, self.now)

    def test_exact_ten_retained_and_accepted(self):
        self.assertEqual(len(self.e['exceptions']), 10)
        result = self.evaluate()
        self.assertTrue(result['passed'])
        self.assertEqual(result['findings'], [finding | {'accepted': True} for finding in self.controls])
        sarif = policy.sarif(result, 'gosec')
        self.assertEqual(len(sarif['runs'][0]['results']), 10)
        self.assertTrue(all(row['properties']['policyAccepted'] for row in sarif['runs'][0]['results']))

    def test_missing_each_control_blocks_only_that_identity(self):
        original = copy.deepcopy(self.e)
        for finding in self.controls:
            self.e = copy.deepcopy(original)
            self.e['exceptions'] = [row for row in self.e['exceptions']
                                    if row['fingerprint'] != finding['fingerprint']]
            with self.subTest(path=finding['path'], line=finding['line']):
                result = self.evaluate()
                self.assertFalse(result['passed'])
                self.assertEqual(len(result['findings']), 10)
                self.assertEqual(sum(row['accepted'] for row in result['findings']), 9)

    def test_changed_each_whole_source_digest_rejects(self):
        for relative in sorted({row['path'] for row in self.controls}):
            path = self.root / relative
            original = path.read_bytes()
            path.write_bytes(original + b'\n// unrelated source drift must trigger re-review\n')
            with self.subTest(path=relative), self.assertRaises(ValueError):
                self.evaluate()
            path.write_bytes(original)

    def test_new_same_file_g703_site_rejected_and_retained(self):
        original = copy.deepcopy(self.g)
        for finding in self.controls:
            self.g = copy.deepcopy(original)
            self.g['Issues'].append({
                'severity': 'HIGH', 'rule_id': 'G703', 'file': finding['path'],
                'line': str(finding['line']), 'column': str(finding['column'] + 1000),
                'code': 'synthetic lower-trust path value must not be retained',
            })
            with self.subTest(path=finding['path'], line=finding['line']):
                result = self.evaluate()
                self.assertFalse(result['passed'])
                self.assertEqual(len(result['findings']), 11)
                self.assertEqual(sum(row['accepted'] for row in result['findings']), 10)
                self.assertNotIn('synthetic lower-trust', json.dumps(result))

    def test_expiry_rejects_at_exact_deadline(self):
        self.now = dt.datetime(2026, 10, 16, tzinfo=dt.timezone.utc)
        with self.assertRaises(ValueError):
            self.evaluate()

    def test_wrong_scope_rejects(self):
        with self.assertRaises(ValueError):
            self.evaluate('release-tag-or-upstream-cve')

    def test_new_rule_same_file_still_blocks(self):
        finding = self.controls[0]
        self.g['Issues'].append({'severity': 'HIGH', 'rule_id': 'G115',
                                'file': finding['path'], 'line': str(finding['line']),
                                'column': str(finding['column'])})
        result = self.evaluate()
        self.assertFalse(result['passed'])
        self.assertEqual(sum(row['accepted'] for row in result['findings']), 10)

    def test_no_control_does_not_accept_any_reviewed_site(self):
        self.e['exceptions'] = []
        result = self.evaluate()
        self.assertFalse(result['passed'])
        self.assertEqual(len(result['findings']), 10)
        self.assertFalse(any(row['accepted'] for row in result['findings']))


if __name__ == '__main__':
    unittest.main()
