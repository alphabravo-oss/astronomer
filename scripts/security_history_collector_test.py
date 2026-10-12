"""Controlled collector contracts; no native scanner or Go binary is launched."""
import importlib.util
import json
from pathlib import Path
import tempfile
import types
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('collector', Path(__file__).with_name('security-history-collector.py'))
c = importlib.util.module_from_spec(spec)
spec.loader.exec_module(c)


class CollectorTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.repo = self.base / 'repo'
        self.repo.mkdir()
        self.go = self.base / 'selected-go'
        self.scanner = self.base / 'selected-gitleaks'
        self.config = self.base / 'config.toml'
        for path in (self.go, self.scanner, self.config):
            path.write_bytes(b'controlled fixture identity\n')
        self.manifest = {'scanner': {'executable_sha256': c.digest(self.scanner), 'config_sha256': c.digest(self.config)}}
        self.output = self.base / 'new-output'
        self.report = [{'Commit': '1' * 40, 'File': 'synthetic.go', 'RuleID': 'generic-api-key', 'Fingerprint': 'synthetic:1',
                        'StartLine': 1, 'EndLine': 1, 'StartColumn': 1, 'EndColumn': 8,
                        'Secret': 'REDACTED', 'Match': 'synthetic=REDACTED'}]
        self.exit = 42
        self.commands = []
        self.guard_stages = []
        self.change = None

    def run_owned(self, command, log, _cwd, _env, capacity, _timeout, **_options):
        self.commands.append(command)
        capacity('prelaunch')
        if '-m' in command:
            raw = str(self.scanner) + ': go1.27.2\n\tmod\tgithub.com/zricethezav/gitleaks/v8\tv8.30.1\th1:synthetic\n'
        elif command == [str(self.go), 'version']:
            raw = c.GO_VERSION + '\n'
        elif command[0] == 'git':
            raw = ('untracked.go\n' if self.change=='dirty' else '') if command[1]=='status' else ('1'*40+'\n' if command[1]=='rev-parse' else '1'*40+' HEAD\n')
            if self.change == 'refs' and command[1]=='show-ref' and log.name.startswith('after-'):
                raw = '2' * 40 + ' HEAD\n'
            if self.change == 'head' and command[1]=='rev-parse' and log.name.startswith('after-'):
                raw = '2'*40+'\n'
        elif command[0] == 'python3':
            source = '2' * 64 if self.change == 'source' and log.name.startswith('after-') else '1' * 64
            raw = json.dumps({'schema_version': 2, 'source_tree_sha256': source})
        else:
            self.assertEqual(command[0], str(self.scanner))
            target = next(item.removeprefix('--report-path=') for item in command if item.startswith('--report-path='))
            if self.change != 'missing-report':
                Path(target).write_bytes(json.dumps(self.report).encode())
            if self.change == 'binary':
                self.scanner.write_bytes(b'changed identity\n')
            raw = ''
        log.write_text(raw)
        capacity('maintained')
        return dict(exitCode=self.exit if command[0] == str(self.scanner) else 0, reason='',
                    guardException=None, drained=True, leaderHeldUntilDrain=True, liveDescendantsAfterDrain=[])

    def guard(self, stage):
        self.guard_stages.append(stage)
        if self.change == 'initial-guard' and stage == 'initial':
            raise RuntimeError('controlled fatal guard')
        if self.change == 'final-guard' and stage == 'collector-final':
            raise RuntimeError('controlled fatal guard')

    def collect(self):
        owned = types.SimpleNamespace(run_owned=self.run_owned)
        with mock.patch.object(c, 'load_owned', return_value=owned):
            return c.collect(self.repo, self.scanner, self.go, c.digest(self.go), self.config,
                             self.manifest, self.output, guard=self.guard)

    def test_actual_collection_fields_come_from_controlled_steps_and_files(self):
        receipt = self.collect()
        self.assertTrue(receipt['collection_complete'])
        collection = json.loads((self.output / 'collection.json').read_text())
        self.assertEqual(collection['report_sha256'], c.digest(self.output / 'gitleaks.json'))
        self.assertEqual(collection['scanner_sha256'], c.digest(self.scanner))
        self.assertEqual(collection['source_before_sha256'], collection['source_after_sha256'])
        self.assertEqual(collection['refs_before_sha256'], collection['refs_after_sha256'])
        command = next(command for command in self.commands if command[0] == str(self.scanner))
        for flag in ('--redact=100', '--exit-code=42', '--report-format=json', '--log-opts=--all'):
            self.assertIn(flag, command)
        self.assertIn('collector-final', self.guard_stages)

    def test_exit_zero_requires_empty_report(self):
        self.exit = 0
        self.report = []
        self.assertTrue(self.collect()['collection_complete'])

    def test_error_exit_preserves_report_but_never_qualifies_partial_collection(self):
        self.exit = 1
        self.assertFalse(self.collect()['collection_complete'])
        self.assertEqual(json.loads((self.output / 'gitleaks.json').read_text()), self.report)

    def test_refs_source_and_binary_drift_fail_closed(self):
        for change in ('refs', 'source', 'head', 'binary', 'dirty'):
            with self.subTest(change=change):
                self.output = self.base / ('new-' + change)
                self.change = change
                self.scanner.write_bytes(b'controlled fixture identity\n')
                self.assertFalse(self.collect()['collection_complete'])

    def test_partial_unredacted_duplicate_and_malformed_site_reports_fail_closed(self):
        original = self.report
        changes = {'duplicate': original * 2, 'unredacted': [dict(original[0], Secret='synthetic-not-redacted')],
                   'bad-coordinate': [dict(original[0], StartLine=True)], 'bad-path': [dict(original[0], File='../escape')]}
        for change, rows in changes.items():
            with self.subTest(change=change):
                self.output = self.base / ('new-' + change)
                self.report = rows
                self.assertFalse(self.collect()['collection_complete'])

    def test_missing_report_and_fatal_guards_keep_structured_failure(self):
        for change in ('missing-report', 'initial-guard', 'final-guard'):
            with self.subTest(change=change):
                self.output = self.base / ('new-' + change)
                self.change = change
                self.scanner.write_bytes(b'controlled fixture identity\n')
                self.assertFalse(self.collect()['collection_complete'])
                self.assertTrue((self.output / 'collector-receipt.json').exists())

    def test_existing_output_is_never_reused_or_overwritten(self):
        self.output.mkdir()
        sentinel = self.output / 'keep.txt'
        sentinel.write_text('owned prior receipt')
        with self.assertRaises(FileExistsError):
            self.collect()
        self.assertEqual(sentinel.read_text(), 'owned prior receipt')


if __name__ == '__main__':
    unittest.main()
