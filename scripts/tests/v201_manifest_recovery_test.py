import base64
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]

def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / filename)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value

recovery = module('recovery', 'recover-v201-manifest.py')
metadata = module('metadata', 'verify-release-metadata.py')


class OriginalSignatureRecoveryTest(unittest.TestCase):
    def setUp(self):
        self.manifest = json.dumps({'release': {'version': recovery.TAG, 'source_commit': recovery.COMMIT}}).encode()
        self.digest = hashlib.sha256(self.manifest).hexdigest()
        self.body = {'kind': 'hashedrekord', 'spec': {'data': {'hash': {'algorithm': 'sha256', 'value': self.digest}},
                     'signature': {'content': 'original-signature', 'publicKey': {'content': 'original-certificate'}}}}
        self.entry = {'body': base64.b64encode(json.dumps(self.body).encode()).decode(), 'logIndex': recovery.LOG_INDEX,
                      'integratedTime': 1, 'logID': 'original-log', 'verification': {'signedEntryTimestamp': 'original-promise'}}

    def recover(self, entry=None, manifest=None):
        with patch.object(recovery, 'MANIFEST_SHA', self.digest):
            return recovery.original_bundle({'entry': entry or self.entry}, manifest or self.manifest)

    def test_preserves_original_signature_certificate_and_signed_payload(self):
        result = self.recover()
        self.assertEqual(result['base64Signature'], 'original-signature')
        self.assertEqual(result['cert'], 'original-certificate')
        self.assertEqual(result['rekorBundle']['SignedEntryTimestamp'], 'original-promise')
        self.assertEqual(result['rekorBundle']['Payload']['body'], self.entry['body'])

    def test_changed_manifest_or_wrong_log_entry_fails(self):
        with self.assertRaises(ValueError): self.recover(manifest=self.manifest + b' ')
        with self.assertRaises(ValueError): self.recover(entry=dict(self.entry, logIndex=1))
        for change in ({'kind': 'dsse'}, {'spec': {'data': {'hash': {'algorithm': 'sha256', 'value': '0' * 64}}}}):
            body = dict(self.body, **change)
            entry = dict(self.entry, body=base64.b64encode(json.dumps(body).encode()).decode())
            with self.subTest(change=change), self.assertRaises(ValueError): self.recover(entry=entry)

    def test_only_exact_interrupted_release_is_allowed(self):
        recovery.require_release(recovery.TAG, recovery.COMMIT, recovery.SOURCE_RUN)
        for args in [('v2.0.2', recovery.COMMIT, recovery.SOURCE_RUN), (recovery.TAG, 'a'*40, recovery.SOURCE_RUN),
                     (recovery.TAG, recovery.COMMIT, '999')]:
            with self.subTest(args=args), self.assertRaises(ValueError): recovery.require_release(*args)


class RuntimeRecoveryTrustTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in ('release-manifest.json', 'runtime-image-evidence.json', 'runtime-image-evidence.tar.gz'):
            (self.root / name).write_text(name)
        original_sha = metadata.sha
        self.hash = patch.object(metadata, 'sha', side_effect=lambda p: 'sha256:' + recovery.MANIFEST_SHA if p.name == 'release-manifest.json' else original_sha(p))
        self.hash.start(); self.addCleanup(self.hash.stop)
        self.record = dict(schema_version=1, tag=recovery.TAG, source_commit=recovery.COMMIT, source_run_id=recovery.SOURCE_RUN,
                           producer_run_id='999', producer_commit='b'*40,
                           files={p.name: metadata.sha(p) for p in self.root.iterdir()})
        (self.root / 'runtime-qualification-recovery.sigstore.json').write_text('signed-proof')
        self.write()
        mock = patch.object(metadata, 'verify_signature')
        self.verify = mock.start(); self.addCleanup(mock.stop)

    def write(self):
        (self.root / 'runtime-qualification-recovery.json').write_text(json.dumps(self.record))

    def validate(self, **kwargs):
        return metadata.runtime_identity(self.root, recovery.TAG, **kwargs)

    def test_only_runtime_report_changes_signer(self):
        self.assertEqual(self.validate(source_commit=recovery.COMMIT, source_run_id=recovery.SOURCE_RUN), metadata.RECOVERY_IDENTITY)
        self.verify.assert_called_once()
        self.assertEqual(metadata.metadata_identity(self.root, recovery.TAG), recovery.IDENTITY)

    def test_unsigned_or_partial_proof_fails(self):
        self.verify.side_effect = subprocess.CalledProcessError(1, 'cosign')
        with self.assertRaises(subprocess.CalledProcessError): self.validate()
        (self.root / 'runtime-qualification-recovery.sigstore.json').unlink()
        with self.assertRaises(ValueError): self.validate()

    def test_wrong_release_source_or_report_bytes_fails(self):
        for field, value in [('tag', 'v2.0.2'), ('source_commit', 'c'*40), ('source_run_id', '100'),
                             ('producer_run_id', 'bad'), ('producer_commit', 'bad'), ('schema_version', 2),
                             ('certificate_identity', 'attacker')]:
            old = copy.deepcopy(self.record); self.record[field] = value; self.write()
            with self.subTest(field=field), self.assertRaises(ValueError): self.validate()
            self.record = old; self.write()
        for name in ('runtime-image-evidence.json', 'runtime-image-evidence.tar.gz'):
            path = self.root / name; original = path.read_bytes(); path.write_bytes(original + b'changed')
            with self.subTest(name=name), self.assertRaises(ValueError): self.validate()
            path.write_bytes(original)
        for kwargs in ({'source_commit': 'c'*40}, {'source_run_id': '100'}):
            with self.subTest(kwargs=kwargs), self.assertRaises(ValueError): self.validate(**kwargs)

    def test_changed_original_manifest_hash_is_rejected(self):
        self.record['files']['release-manifest.json'] = 'sha256:' + 'a'*64; self.write()
        with self.assertRaises(ValueError): self.validate()

    def test_workflow_preserves_original_manifest_and_collects_runtime_proof(self):
        workflow = (ROOT / '.github/workflows/resume-release.yaml').read_text()
        self.assertIn(recovery.TAG + ':' + recovery.COMMIT + ':' + recovery.SOURCE_RUN, workflow)
        self.assertIn('qualification-tools/scripts/recover-v201-manifest.py', workflow)
        self.assertIn('[[ "$IMAGE_TAG" != v2.0.1 ]] || waivers=qualification-tools/deploy/release/runtime-image-waivers.json', workflow)
        self.assertIn('qualification_assets+=(runtime-qualification-recovery.json runtime-qualification-recovery.sigstore.json)', workflow)


if __name__ == '__main__': unittest.main()
