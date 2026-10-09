#!/usr/bin/env python3
import copy
import datetime as dt
import importlib.util
import json
from pathlib import Path
import unittest
ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('tool',ROOT/'scripts/check-gosec-tool.py');tool=importlib.util.module_from_spec(spec);spec.loader.exec_module(tool)
class ToolContractTest(unittest.TestCase):
    def setUp(self):
        p=ROOT/'tools/gosec';self.record=json.loads((p/'closure.json').read_text());self.mod=(p/'go.mod').read_bytes();self.sums=(p/'go.sum').read_bytes();self.now=dt.datetime(2026,10,9,tzinfo=dt.timezone.utc)
    def test_bound_released_closure(self): self.assertTrue(tool.validate(self.record,self.mod,self.sums,self.now))
    def test_changed_module_lock_metadata_expiry(self):
        for mod,sums in ((self.mod+b'\n',self.sums),(self.mod,self.sums+b'\n')):
            with self.assertRaises(ValueError):tool.validate(self.record,mod,sums,self.now)
        for update in ({'scanner_version':'v2.28.0'},{'compiler':'1.26.6'},{'x_tools_version':'v0.49.0'},{'review_until':'2026-10-08T00:00:00Z'},{'unknown':True}):
            record=copy.deepcopy(self.record);record.update(update)
            with self.subTest(update=update),self.assertRaises(ValueError):tool.validate(record,self.mod,self.sums,self.now)
    def test_replaced_scanner_source_forbidden_even_rebound(self):
        mod=self.mod+b'\nreplace github.com/securego/gosec/v2 => /tmp/synthetic\n';record=copy.deepcopy(self.record);record['go_mod_sha256']=tool.sha(mod)
        with self.assertRaises(ValueError):tool.validate(record,mod,self.sums,self.now)
if __name__=='__main__': unittest.main()
