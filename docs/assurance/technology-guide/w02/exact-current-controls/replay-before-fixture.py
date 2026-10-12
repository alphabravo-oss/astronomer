"""Replay the test-first exception bytes without mutating the current source."""
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

root = Path.cwd().resolve()
spec = importlib.util.spec_from_file_location('tests', root/'scripts/tests/security_source_policy_test.py')
tests = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tests)
with tempfile.TemporaryDirectory(prefix='w02-controls-before-') as directory:
    old = Path(directory)
    evidence = Path('docs/assurance/technology-guide/w02/current-source-927151be')
    paths = [Path('deploy/security/source-policy.json'), evidence/'sql-control-current-binding-proposal.json', evidence/'fips-random-nonce-control-proposal.json']
    inputs = json.loads((root/paths[1]).read_text())
    nonce = json.loads((root/paths[2]).read_text())
    paths += [Path(row['path']) for row in inputs['checks']] + [Path(nonce['finding']['path'])]
    for relative in set(paths):
        destination = old/relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(root/relative, destination)
    (old/'deploy/security/source-exceptions.json').write_bytes(subprocess.check_output(['git', 'show', 'e3ac5fdf:deploy/security/source-exceptions.json'], cwd=root))
    tests.ROOT = old
    result = unittest.TextTestRunner(verbosity=2).run(unittest.TestSuite([tests.ReviewedSourceControlsTest('test_all_63_controls_are_exact_and_retained')]))
    if result.wasSuccessful() or len(result.failures) != 1 or result.errors:
        raise SystemExit('unexpected baseline fixture result')
    raise SystemExit(1)
