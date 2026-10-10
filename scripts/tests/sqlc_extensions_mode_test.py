"""Source generation keeps tracked modes and uses public-source mode for new Go."""
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SOURCE = Path(__file__).resolve().parents[1] / 'generate-sqlc-extensions.py'
spec = importlib.util.spec_from_file_location('sqlc_extensions', SOURCE)
generator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(generator)

class AtomicModeTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / 'generated.go'

    def test_existing_public_source_mode_is_preserved(self):
        self.path.write_text('before\n')
        self.path.chmod(0o644)
        generator.atomic_write(self.path, 'after\n')
        self.assertEqual(self.path.read_text(), 'after\n')
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o644)

    def test_existing_restrictive_mode_is_not_broadened(self):
        self.path.write_text('before\n')
        self.path.chmod(0o600)
        generator.atomic_write(self.path, 'after\n')
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o600)

    def test_new_source_is_readable_under_restrictive_umask(self):
        previous = os.umask(0o077)
        try:
            generator.atomic_write(self.path, 'generated public source\n')
        finally:
            os.umask(previous)
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o644)
        self.assertEqual(self.path.read_text(), 'generated public source\n')

    def test_replace_failure_preserves_old_bytes_mode_and_removes_temp(self):
        self.path.write_text('before\n')
        self.path.chmod(0o644)
        with mock.patch.object(generator.os, 'replace', side_effect=OSError('synthetic replacement failure')):
            with self.assertRaises(OSError):
                generator.atomic_write(self.path, 'after\n')
        self.assertEqual(self.path.read_text(), 'before\n')
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o644)
        self.assertEqual(list(self.path.parent.iterdir()), [self.path])

if __name__ == '__main__':
    unittest.main(verbosity=2)
