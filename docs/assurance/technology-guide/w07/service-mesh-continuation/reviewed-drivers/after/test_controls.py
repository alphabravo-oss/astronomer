import copy
import json
from pathlib import Path
import tempfile
import unittest
import run_after as runner

class InventoryControls(unittest.TestCase):
    def rows(self):
        rows=[]
        for name in sorted(runner.EXPECTED):
            rows.extend([{'Action':'run','Test':name,'Package':runner.PACKAGE},{'Action':'pass','Test':name,'Package':runner.PACKAGE}])
        rows.append({'Action':'pass','Package':runner.PACKAGE})
        return rows
    def verify(self,rows):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'events.log';p.write_text(''.join(json.dumps(r)+'\n' for r in rows));return runner.verify_after(p)
    def test_complete_45_inventory(self):
        self.assertEqual(self.verify(self.rows())['named_pass'],45)
    def test_missing_duplicate_unknown_run_reject(self):
        for kind in ('missing','duplicate','unknown'):
            rows=self.rows()
            if kind=='missing':rows.pop(0)
            elif kind=='duplicate':rows.insert(0,copy.deepcopy(rows[0]))
            else:rows[0]['Test']='TestUnknown'
            with self.subTest(kind=kind),self.assertRaises(AssertionError):self.verify(rows)
    def test_missing_duplicate_skip_failed_terminal_reject(self):
        for kind in ('missing','duplicate','skip','fail'):
            rows=self.rows()
            if kind=='missing':rows.pop(1)
            elif kind=='duplicate':rows.insert(1,copy.deepcopy(rows[1]))
            else:rows[1]['Action']=kind
            with self.subTest(kind=kind),self.assertRaises(AssertionError):self.verify(rows)
    def test_package_drift_or_contradiction_reject(self):
        for kind in ('missing','wrong','failed','duplicate'):
            rows=self.rows()
            if kind=='missing':rows[0].pop('Package')
            elif kind=='wrong':rows[0]['Package']='other'
            elif kind=='failed':rows[-1]['Action']='fail'
            else:rows.append(copy.deepcopy(rows[-1]))
            with self.subTest(kind=kind),self.assertRaises(AssertionError):self.verify(rows)
    def test_runtime_build_setup_or_non_event_reject(self):
        for row in ({'Action':'build-fail','Package':runner.PACKAGE},{'Action':'output','Output':'DATA RACE','Package':runner.PACKAGE},{'Action':'output','Output':'cleanup failure','Package':runner.PACKAGE},{'message':'fault'}):
            rows=self.rows();rows.insert(0,row)
            with self.subTest(row=row),self.assertRaises(AssertionError):self.verify(rows)
    def test_malformed_or_relaxed_guard_reject(self):
        valid={'total_kib':100000000,'used_kib':70000000,'available_kib':30000000,'available_gib':28,'free_percent':30,'minimum_free_gib':20,'minimum_free_percent':20,'meets_minimum':True}
        runner.verify_guard(json.dumps(valid),0)
        for key,value in (('minimum_free_percent',19),('minimum_free_gib',1),('available_kib',False),('meets_minimum',False)):
            row=dict(valid);row[key]=value
            with self.subTest(key=key),self.assertRaises(AssertionError):runner.verify_guard(json.dumps(row),0)
if __name__=='__main__':unittest.main()
