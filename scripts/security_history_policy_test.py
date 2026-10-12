import copy
import os
import hashlib
import datetime as dt
import gzip
import importlib.util
import json
import re
import tomllib
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('history', Path(__file__).with_name('security-history-policy.py'))
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)

class HistoryPolicyTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.commit = 'a' * 40
        self.body = b'package fixture\n'
        self.inventory = policy.canonical({'source_inputs': {'auth.go': policy.sha(self.body)}})
        self.site = dict(Commit=self.commit, File='inventory.json', RuleID='generic-api-key',
                         Fingerprint='fixture-only', StartLine=1, EndLine=1,
                         StartColumn=1, EndColumn=1)
        proof = dict(field='/source_inputs/auth.go', referenced_path='auth.go',
                     referenced_commit=self.commit, referenced_blob_sha256=policy.sha(self.body),
                     field_components=['source_inputs', 'auth.go'],
                     field_rfc6901='/source_inputs/auth.go')
        self.rule_config = Path(os.environ.get('ASTRONOMER_HISTORY_FIXTURE_RULE_CONFIG', str(Path(__file__).resolve().parents[1]/'docs/assurance/technology-guide/w02/retained-history-policy/evidence-bundle/scanner-source/0-gitleaks.toml')))
        assert hashlib.sha256(self.rule_config.read_bytes()).hexdigest() == 'e163e53b9e7e8a8511e77271e2b323ed057759542a6d988258afe3a1fa329caf', 'fixture rule config drift'
        config = tomllib.loads(self.rule_config.read_text())
        self.pattern = re.compile(next(row['regex'] for row in config['rules'] if row['id'] == 'generic-api-key'))
        match = self.pattern.search(self.inventory.decode())
        self.site.update(StartColumn=match.start()+1, EndColumn=match.end())
        column = dict(actual_blob_columns=[match.start()+1, match.end()], span_sha256=policy.sha(match[0].encode()),
                      reviewed_origin='Synthetic immutable fixture only', replay_row_sha256='0'*64)
        self.control = dict(index=0, site=self.site, blob_sha256=policy.sha(self.inventory),
                            classification='verified-public-file-digest', proof=proof,
                            proof_sha256=policy.sha(policy.canonical(proof)), column_proof=column,
                            column_proof_sha256=policy.sha(policy.canonical(column)))
        self.registry = {'schema_version': 1, 'records': [self.control]}
        self.manifest = dict(schema_version=1, record_count=1, registry_gzip_sha256='0' * 64,
                             registry_json_sha256='0' * 64, original_report_gzip_sha256='1' * 64,
                             original_report_json_sha256='2' * 64, site_evidence_sha256='3' * 64,
                             column_evidence_sha256='4' * 64,
                             scanner=dict(module='github.com/zricethezav/gitleaks/v8 v8.30.1',
                                          executable_sha256='5' * 64, config_sha256='6' * 64,
                                          column_source_sha256={'/selected/config/gitleaks.toml': policy.sha(self.rule_config.read_bytes())}),
                             review_owner='synthetic fixture; no release approver',
                             review_evidence=[{'path': 'synthetic-review.json', 'sha256': '8' * 64}],
                             expires_at='2026-10-16T00:00:00Z', release_scope='fixture')
        self.findings = [dict(self.site, Secret='REDACTED', Match='synthetic fixture REDACTED')]
        self.collection = dict(schema_version=1, scanner_sha256='5' * 64,
                               scanner_module=self.manifest['scanner']['module'], config_sha256='6' * 64,
                               exit_code=42, redact_percent=100, log_opts='--all', report_sha256='0' * 64,
                               refs_before_sha256='9' * 64, refs_after_sha256='9' * 64,
                               source_before_sha256='b' * 64, source_after_sha256='b' * 64,
                               collection_complete=True)
        owner = self
        class Evidence:
            rule_config_sha256 = policy.sha(owner.rule_config.read_bytes())
            def capture(self, site, line, start, end):
                matches = [m for m in owner.pattern.finditer(line) if m.start()+1 == start and m.end() == end]
                policy.require(len(matches) == 1)
                return matches[0]
            def blob(self, commit, path):
                policy.require(commit == owner.commit)
                return {'inventory.json': owner.inventory, 'auth.go': owner.body}[path]
        self.evidence = Evidence()

    def inputs(self):
        original = [dict(self.site, Secret='REDACTED', Match='fixture REDACTED')]
        compressed = gzip.compress(policy.canonical(original), mtime=0)
        site = dict(index=0, fingerprint=self.site['Fingerprint'], commit=self.commit, path=self.site['File'],
                    rule=self.site['RuleID'], line=self.site['StartLine'], column_start=self.site['StartColumn'],
                    column_end=self.site['EndColumn'], historical_blob_sha256=self.control['blob_sha256'],
                    classification=self.control['classification'], proof=copy.deepcopy(self.control['proof']))
        match = self.pattern.search(self.inventory.decode())
        capture = dict(index=0, fingerprint=self.site['Fingerprint'], historical_blob_sha256=self.control['blob_sha256'],
                       proof=dict(actual_blob_columns=self.control['column_proof']['actual_blob_columns'],
                                  token_columns=[match.start(1)+1,match.end(1)], token_sha256=policy.sha(match[1].encode())))
        self.control['column_proof']['replay_row_sha256'] = policy.sha(policy.canonical(capture))
        self.control['column_proof_sha256'] = policy.sha(policy.canonical(self.control['column_proof']))
        columns = dict(report_compressed_sha256=policy.sha(compressed),
                       report_raw_sha256=policy.sha(policy.canonical(original)),
                       source_proof_hashes=self.manifest['scanner']['column_source_sha256'], records=[])
        values = {'original.gz': compressed, 'sites.json': policy.canonical({'report_compressed_sha256': policy.sha(compressed), 'records': [site]}),
                  'columns.json': policy.canonical(columns), 'replay.json': policy.canonical(dict(columns, records=[capture])), 'config.toml': self.rule_config.read_bytes(), 'review.json': b'{}'}
        descriptors = {}
        for name, data in values.items():
            (self.directory/name).write_bytes(data)
            descriptors[name] = {'path': name, 'sha256': policy.sha(data)}
        self.manifest.update(original_report_gzip_sha256=policy.sha(compressed),
                             original_report_json_sha256=policy.sha(policy.canonical(original)),
                             site_evidence_sha256=descriptors['sites.json']['sha256'],
                             column_evidence_sha256=descriptors['columns.json']['sha256'],
                             column_replay_sha256=descriptors['replay.json']['sha256'],
                             review_evidence=[descriptors['review.json']],
                             inputs={'original_report': descriptors['original.gz'], 'site_evidence': descriptors['sites.json'],
                                     'column_evidence': descriptors['columns.json'], 'column_replay': descriptors['replay.json'],
                                     'column_sources': {'/selected/config/gitleaks.toml': descriptors['config.toml']}})

    def controls(self):
        self.inputs()
        raw = policy.canonical(self.registry)
        compressed = gzip.compress(raw, mtime=0)
        path = self.directory / 'registry.json.gz'
        path.write_bytes(compressed)
        self.manifest['registry_json_sha256'] = policy.sha(raw)
        self.manifest['registry_gzip_sha256'] = policy.sha(compressed)
        return policy.load_registry(self.manifest, path, 'fixture', dt.datetime(2026, 10, 10, tzinfo=dt.timezone.utc), self.directory)

    def evaluate(self):
        report = policy.canonical(self.findings)
        self.collection['report_sha256'] = policy.sha(report)
        return policy.evaluate(report, self.collection, self.manifest, self.controls(), 'fixture', self.evidence)

    def test_exact_positive_retains_order_without_values(self):
        result = self.evaluate()
        self.assertTrue(result['passed'])
        self.assertEqual(result['accepted_count'], 1)
        self.assertEqual(result['findings'][0]['index'], 0)
        self.assertNotIn('Match', json.dumps(result))
        self.assertNotIn('Secret', json.dumps(result))

    def test_new_credential_same_file_field_new_commit_and_new_file_block(self):
        original = copy.deepcopy(self.findings)
        for update in ({'Commit': 'c' * 40}, {'File': 'new_credentials.json'},
                       {'StartColumn': 2}, {'RuleID': 'private-key'}, {'Fingerprint': 'new-finding'}):
            self.findings = original + [dict(original[0], **update)]
            with self.subTest(update=update):
                result = self.evaluate()
                self.assertFalse(result['passed'])
                self.assertEqual(result['finding_count'], 2)
                self.assertEqual(result['accepted_count'], 1)
        self.findings = original

    def test_unknown_missing_duplicate_and_nonredacted_records_block(self):
        self.findings = []
        self.collection['exit_code'] = 0
        self.assertFalse(self.evaluate()['passed'])
        self.collection['exit_code'] = 42
        self.findings = [dict(self.site, Secret='REDACTED', Match='fixture REDACTED')] * 2
        with self.assertRaises(ValueError): self.evaluate()
        for update in ({'Secret': 'synthetic nonredacted credential'}, {'Match': 'no redaction marker'}):
            self.findings = [dict(self.site, Secret='REDACTED', Match='fixture REDACTED', **{})]
            self.findings[0].update(update)
            with self.subTest(update=update), self.assertRaises(ValueError): self.evaluate()

    def test_source_blob_referenced_public_file_and_proof_mutations_block(self):
        self.body += b'changed\n'
        with self.assertRaises(ValueError): self.evaluate()
        self.body = b'package fixture\n'
        self.inventory += b' '
        with self.assertRaises(ValueError): self.evaluate()
        self.inventory = policy.canonical({'source_inputs': {'auth.go': policy.sha(self.body)}})
        self.control['proof']['referenced_blob_sha256'] = '0' * 64
        with self.assertRaises(ValueError): self.evaluate()
        self.control['proof_sha256'] = policy.sha(policy.canonical(self.control['proof']))
        with self.assertRaises(ValueError): self.evaluate()

    def test_registry_count_order_unknown_fields_and_boolean_version_block(self):
        for mutate in (lambda: self.registry.update(schema_version=True),
                       lambda: self.registry.update(unknown=True),
                       lambda: self.control.update(index=1),
                       lambda: self.control.update(wildcard='*'),
                       lambda: self.control['column_proof'].update(actual_blob_columns=[0, 2])):
            registry, manifest = copy.deepcopy(self.registry), copy.deepcopy(self.manifest)
            mutate()
            with self.assertRaises(ValueError): self.evaluate()
            self.registry, self.manifest = registry, manifest
            self.control = self.registry['records'][0]
        self.manifest['record_count'] = 2
        with self.assertRaises(ValueError): self.controls()

    def test_registry_hash_expiry_scope_and_review_schema_block(self):
        self.controls()
        self.manifest['registry_gzip_sha256'] = '0' * 64
        with self.assertRaises(ValueError):
            policy.load_registry(self.manifest, self.directory/'registry.json.gz', 'fixture', dt.datetime(2026, 10, 10, tzinfo=dt.timezone.utc), self.directory)
        for update in ({'expires_at': '2026-10-10T00:00:00Z'}, {'release_scope': 'other'},
                       {'schema_version': True}, {'review_owner': ''}, {'unbounded_path_ignore': '*'}):
            saved = copy.deepcopy(self.manifest)
            self.manifest.update(update)
            with self.subTest(update=update), self.assertRaises(ValueError): self.controls()
            self.manifest = saved

    def test_partial_scan_provenance_redaction_refs_and_source_drift_block(self):
        for update in ({'exit_code': 1}, {'exit_code': -9}, {'exit_code': 0},
                       {'collection_complete': False}, {'redact_percent': 99}, {'log_opts': ''},
                       {'scanner_sha256': '0' * 64}, {'config_sha256': '0' * 64},
                       {'scanner_module': 'different scanner'}, {'refs_after_sha256': '0' * 64},
                       {'source_after_sha256': '0' * 64}, {'schema_version': True}, {'extra': True}):
            saved = copy.deepcopy(self.collection)
            self.collection.update(update)
            with self.subTest(update=update), self.assertRaises(ValueError): self.evaluate()
            self.collection = saved

    def test_actual_input_bytes_must_match_all_claimed_digests(self):
        for name in ['original.gz', 'sites.json', 'columns.json', 'replay.json', 'config.toml', 'review.json']:
            self.controls()
            path = self.directory/name
            path.write_bytes(path.read_bytes()+b' ')
            with self.subTest(artifact=name), self.assertRaises(ValueError):
                policy.load_registry(self.manifest, self.directory/'registry.json.gz', 'fixture', dt.datetime(2026,10,10,tzinfo=dt.timezone.utc),self.directory)

    def test_valid_digest_cannot_replace_original_site_or_proof_binding(self):
        for artifact, mutate in [('sites.json', lambda x: x['records'][0].update(classification='public-git-commit')),
                                 ('replay.json', lambda x: x['records'][0]['proof'].update(actual_blob_columns=[1,2]))]:
            self.controls(); path=self.directory/artifact; value=json.loads(path.read_bytes());mutate(value)
            raw=policy.canonical(value);path.write_bytes(raw)
            key='site_evidence' if artifact=='sites.json' else 'column_replay'
            self.manifest['inputs'][key]['sha256']=policy.sha(raw)
            self.manifest[key+'_sha256']=policy.sha(raw)
            with self.subTest(artifact=artifact),self.assertRaises(ValueError):
                policy.load_registry(self.manifest,self.directory/'registry.json.gz','fixture',dt.datetime(2026,10,10,tzinfo=dt.timezone.utc),self.directory)

    def test_report_hash_mismatch_and_duplicate_json_keys_block(self):
        controls = self.controls()
        report = policy.canonical(self.findings)
        with self.assertRaises(ValueError):
            policy.evaluate(report, self.collection, self.manifest, controls, 'fixture', self.evidence)
        with self.assertRaises(ValueError): policy.parse(b'{"field":1,"field":2}')
        with self.assertRaises(ValueError): policy.parse(b'{} {}')
        retained = policy.retain_unaccepted(policy.canonical(self.findings * 2), 'fixture')
        self.assertFalse(retained['passed'])
        self.assertEqual(retained['finding_count'], 2)
        self.assertEqual([row['index'] for row in retained['findings']], [0, 1])
        self.assertTrue(all(not row['accepted'] for row in retained['findings']))

if __name__ == '__main__':
    unittest.main()
