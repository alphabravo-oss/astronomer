import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

DIRECTORY=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('mesh_before',DIRECTORY/'run_before.py')
runner=importlib.util.module_from_spec(spec);spec.loader.exec_module(runner)

class MeshBeforeControls(unittest.TestCase):
    def events(self):
        rows=[{'Action':'start','Package':runner.PACKAGE}]
        for name in sorted(runner.EXPECTED):
            rows.append({'Action':'run','Package':runner.PACKAGE,'Test':name})
            if name in runner.DIAGNOSTICS:rows.append({'Action':'output','Package':runner.PACKAGE,'Test':name,'Output':runner.DIAGNOSTICS[name]+' fixture'})
            rows.append({'Action':'fail','Package':runner.PACKAGE,'Test':name})
        return rows+[{'Action':'fail','Package':runner.PACKAGE}]
    def verify(self,rows):
        with tempfile.TemporaryDirectory() as directory:
            path=Path(directory)/'events';path.write_text(''.join(json.dumps(row)+'\n' for row in rows));return runner.verify_before(path)
    def test_exact_owned_six_failures(self):self.assertEqual(self.verify(self.events())['named_failures'],6)
    def test_missing_duplicate_unknown_and_unterminated_run(self):
        for mutate in [lambda r:r.pop(1),lambda r:r.insert(1,copy.deepcopy(r[1])),lambda r:r[1].update(Test='TestUnknown')]:
            rows=self.events();mutate(rows)
            with self.assertRaises(AssertionError):self.verify(rows)
    def test_terminal_and_package_contradictions(self):
        for mutate in [lambda r:r[-1].update(Action='pass'),lambda r:r.append(copy.deepcopy(r[-1])),lambda r:r[3].update(Action='pass')]:
            rows=self.events();mutate(rows)
            with self.assertRaises(AssertionError):self.verify(rows)
    def test_wrong_package_and_missing_diagnostic(self):
        for mutate in [lambda r:r[0].update(Package='other'),lambda r:next(row for row in r if row['Action']=='output').update(Output='setup could not connect')]:
            rows=self.events();mutate(rows)
            with self.assertRaises(AssertionError):self.verify(rows)
    def test_package_faults_cannot_receive_credit(self):
        for extra in [{'Action':'build-fail','Package':runner.PACKAGE},{'Action':'output','Package':runner.PACKAGE,'Output':'DATA RACE'},{'Action':'output','Package':runner.PACKAGE,'Output':'cleanup failure'},{'unrelated':'object'}]:
            with self.assertRaises(AssertionError):self.verify(self.events()+[extra])
    def test_default_capacity_shape(self):
        row={'total_kib':100*1024*1024,'used_kib':50*1024*1024,'available_kib':50*1024*1024,'available_gib':50,'free_percent':50,'minimum_free_gib':20,'minimum_free_percent':20,'meets_minimum':True}
        runner.verify_guard(json.dumps(row),0)
        for mutate in [lambda r:r.update(minimum_free_percent=10),lambda r:r.update(available_kib=1.5),lambda r:r.update(meets_minimum=False)]:
            changed=copy.deepcopy(row);mutate(changed)
            with self.assertRaises(AssertionError):runner.verify_guard(json.dumps(changed),0)

if __name__=='__main__':unittest.main()
