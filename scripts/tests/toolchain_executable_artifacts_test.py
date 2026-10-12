import copy
import datetime as dt
import importlib.util
import pathlib
import sys
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
SPEC = importlib.util.spec_from_file_location('toolchain', ROOT / 'scripts/toolchain.py')
toolchain = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(toolchain)


class ExecutableArtifactTests(unittest.TestCase):
    def setUp(self):
        self.now = dt.datetime(2026, 10, 10, 18, tzinfo=dt.timezone.utc)
        self.component = {'id': 'k3d', 'selected': '5.9.0', 'platforms': ['linux/amd64', 'linux/arm64'],
                          'sourceBindings': [{'path': 'proof/checksums.txt', 'digest': 'sha256:' + 'a' * 64}]}
        self.component['executableArtifacts'] = [
            {'kind': 'binary', 'version': '5.9.0', 'name': f'k3d-linux-{arch}', 'platform': f'linux/{arch}',
             'url': f'https://github.com/k3d-io/k3d/releases/download/v5.9.0/k3d-linux-{arch}',
             'sizeBytes': 1024, 'digest': 'sha256:' + 'b' * 64, 'provenance': 'proof/checksums.txt',
             'verifiedAt': '2026-10-10T17:00:00Z'} for arch in ['amd64', 'arm64']]

    def test_exact_binary_and_archive_roles(self):
        toolchain.validate_executable_artifacts(self.component, self.now)
        self.component['id'] = 'helm'
        self.component['selected'] = '4.3.0'
        for item in self.component['executableArtifacts']:
            arch = item['platform'].split('/')[1]
            item.update(kind='tar.gz', version='4.3.0', name=f'helm-v4.3.0-linux-{arch}.tar.gz',
                        url=f'https://get.helm.sh/helm-v4.3.0-linux-{arch}.tar.gz', archiveMember=f'linux-{arch}/helm')
        toolchain.validate_executable_artifacts(self.component, self.now)
        self.component['executableArtifacts'][0]['archiveMember'] = '../helm'
        with self.assertRaisesRegex(ValueError, 'extraction role'):
            toolchain.validate_executable_artifacts(self.component, self.now)

    def test_absent_is_compatible_but_null_empty_and_unselected_reject(self):
        without = copy.deepcopy(self.component)
        del without['executableArtifacts']
        toolchain.validate_executable_artifacts(without, self.now)
        for value in [None, [], {}]:
            with self.subTest(value=value):
                document = copy.deepcopy(self.component)
                document['executableArtifacts'] = value
                with self.assertRaises(ValueError):
                    toolchain.validate_executable_artifacts(document, self.now)
        self.component['selected'] = None
        with self.assertRaises(ValueError):
            toolchain.validate_executable_artifacts(self.component, self.now)

    def test_missing_duplicate_and_foreign_platforms_reject(self):
        for change in ['missing', 'duplicate', 'foreign']:
            with self.subTest(change=change):
                document = copy.deepcopy(self.component)
                if change == 'missing':
                    document['executableArtifacts'].pop()
                elif change == 'duplicate':
                    document['executableArtifacts'][1] = copy.deepcopy(document['executableArtifacts'][0])
                else:
                    document['executableArtifacts'][0]['platform'] = 'darwin/amd64'
                with self.assertRaises(ValueError):
                    toolchain.validate_executable_artifacts(document, self.now)

    def test_version_url_name_and_member_reject(self):
        cases = [('version', '5.9.00'), ('url', 'https://github.com/other/k3d/releases/download/v5.9.0/k3d-linux-amd64'),
                 ('url', 'https://user:password@github.com/k3d-io/k3d/releases/download/v5.9.0/k3d-linux-amd64'),
                 ('url', self.component['executableArtifacts'][0]['url'] + '?token=synthetic'),
                 ('name', '../k3d'), ('archiveMember', None), ('kind', 'tar.gz')]
        for key, value in cases:
            with self.subTest(field=key):
                document = copy.deepcopy(self.component)
                document['executableArtifacts'][0][key] = value
                with self.assertRaises(ValueError):
                    toolchain.validate_executable_artifacts(document, self.now)

    def test_finite_integer_size_and_digest_reject(self):
        for key, value in [('sizeBytes', True), ('sizeBytes', 0), ('sizeBytes', -1), ('sizeBytes', 513 << 20),
                           ('sizeBytes', 1.0), ('digest', None), ('digest', 'sha256:' + 'g' * 64)]:
            with self.subTest(field=key, value=value):
                document = copy.deepcopy(self.component)
                document['executableArtifacts'][0][key] = value
                with self.assertRaises(ValueError):
                    toolchain.validate_executable_artifacts(document, self.now)

    def test_missing_source_bound_proof_and_future_time_reject(self):
        for key, value in [('provenance', 'unbound/checksums.txt'), ('verifiedAt', '2026-10-11T00:00:00Z')]:
            with self.subTest(field=key):
                document = copy.deepcopy(self.component)
                document['executableArtifacts'][0][key] = value
                with self.assertRaises(ValueError):
                    toolchain.validate_executable_artifacts(document, self.now)
        self.component['sourceBindings'] = []
        with self.assertRaises(ValueError):
            toolchain.validate_executable_artifacts(self.component, self.now)

    def test_signed_resolution_binds_binary_and_archive_digests(self):
        spec = importlib.util.spec_from_file_location('toolchain_resolution', ROOT / 'scripts/toolchain-resolution.py')
        resolution = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(resolution)
        document = {'components': [{'id': 'k3d', 'selected': '5.9.0',
                                  'discovery': {'version': '5.9.0', 'observedAt': '2026-10-10T17:00:00Z'},
                                  'artifacts': [], 'executableArtifacts': self.component['executableArtifacts']}]}
        before = resolution.bindings(document)
        self.assertEqual(before[0]['artifactDigests'], ['sha256:' + 'b' * 64] * 2)
        document['components'][0]['executableArtifacts'][1]['digest'] = 'sha256:' + 'c' * 64
        self.assertNotEqual(before, resolution.bindings(document))
        del document['components'][0]['executableArtifacts']
        self.assertEqual(resolution.bindings(document)[0]['artifactDigests'], [])
