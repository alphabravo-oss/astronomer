"""Exact evidence exemptions must never exempt adjacent real credential shapes."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]

@unittest.skipUnless(os.environ.get('GITLEAKS_BIN'), 'set GITLEAKS_BIN to the pinned scanner')
class ReleaseEvidenceAllowlistTest(unittest.TestCase):
    def test_public_evidence_and_negative_credential_controls(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            fixture = root/'docs/assurance/technology-guide/release-fixture.json'
            fixture.parent.mkdir(parents=True)
            key = 'SYNTHETIC_' + 'nK8qZ7wE4rT2yU9iO6pA3sD5fG1hJ0lC'
            fixture.write_text(json.dumps({'source': {'api_token.go': 'a'*64}, 'api_key': key}))
            negative = root/'scripts/tests/toolchain_test.py'
            negative.parent.mkdir(parents=True)
            negative.write_text('[("valkey-test-runtime", "9.1.20-alpine"), ("valkey-test-runtime", "9.1.2-malicious")]\napi_key = "'+key+'"\n')
            unrelated = root/'config.json'
            unrelated.write_text(json.dumps({'api_key': key}))
            report = root/'results.json'
            result = subprocess.run([os.environ['GITLEAKS_BIN'], 'dir', '.', '--config', str(ROOT/'.gitleaks.toml'), '--report-format', 'json', '--report-path', str(report), '--redact', '--no-banner'], capture_output=True, text=True, cwd=root)
            self.assertEqual(result.returncode, 1, result.stderr)
            findings = json.loads(report.read_text())
            paths = {Path(f['File']).as_posix() for f in findings}
            for name in ['release-fixture.json', 'toolchain_test.py', 'config.json']:
                self.assertTrue(any(p.endswith(name) for p in paths), name)
            self.assertEqual(len(findings), 3, [(f['File'], f['RuleID']) for f in findings])

if __name__ == '__main__':
    unittest.main()
