#!/usr/bin/env python3
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import shutil
import tempfile
import unittest
ROOT=Path(__file__).resolve().parents[2]
HERE=ROOT/'docs/assurance/technology-guide/w02'
spec=importlib.util.spec_from_file_location('evidence',HERE/'validate-evidence.py');evidence=importlib.util.module_from_spec(spec);spec.loader.exec_module(evidence)
class EvidenceTest(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
        self.path=Path(self.tmp.name)/'w02';shutil.copytree(HERE,self.path);evidence.HERE=self.path
        self.addCleanup(setattr,evidence,'HERE',HERE)
    def run_validation(self):
        with contextlib.redirect_stdout(io.StringIO()):evidence.main()
    def test_retained_binding(self):self.run_validation()
    def test_changed_artifact_rejected(self):
        (self.path/'source-findings.json').write_text('{}')
        with self.assertRaises(ValueError):self.run_validation()
    def test_unsigned_done_rejected(self):
        p=self.path/'verification.json';r=json.loads(p.read_text());r['qualification']='passed';p.write_text(json.dumps(r))
        with self.assertRaises(ValueError):self.run_validation()
    def test_rebound_false_source_input_rejected(self):
        p=self.path/'scanner-compatibility.json';r=json.loads(p.read_text());r['source_inputs']['go.mod']='sha256:'+'0'*64;p.write_text(json.dumps(r))
        v=self.path/'verification.json';record=json.loads(v.read_text());record['artifacts']['scanner-compatibility.json']=evidence.digest(p.read_bytes());v.write_text(json.dumps(record))
        with self.assertRaises(ValueError):self.run_validation()
if __name__=='__main__':unittest.main()
