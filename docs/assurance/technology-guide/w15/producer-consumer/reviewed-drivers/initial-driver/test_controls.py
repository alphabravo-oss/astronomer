import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
import run_interop as r

class InteropControls(unittest.TestCase):
    def events(self):
        rows=[]
        for name in sorted(r.EXPECTED):rows.extend([{'Action':'run','Test':name,'Package':r.PACKAGE},{'Action':'pass','Test':name,'Package':r.PACKAGE}])
        return rows+[{'Action':'pass','Package':r.PACKAGE}]
    def verify(self,rows):
        with tempfile.TemporaryDirectory() as directory:
            p=Path(directory)/'log';p.write_text(''.join(json.dumps(row)+'\n' for row in rows));return r.verify_consumer(p)
    def fixture(self,parent):
        root=parent/'output';root.mkdir(mode=0o700);(root/'entries').mkdir(mode=0o700)
        slugs=['constellation']+['fixture-'+str(n) for n in range(20)]
        catalog={'metadata':{'schemaVersion':1,'minimumReaderVersion':1},'applications':[{'slug':slug} for slug in slugs],'repositories':[{} for _ in range(17)]}
        def write(name,value):
            p=root/name;p.write_text(json.dumps(value,sort_keys=True));p.chmod(0o600)
        for slug in slugs:write('entries/'+slug+'.json',{'slug':slug})
        write('catalog.canonical.json',catalog)
        manifest={'applicationCount':21,'repositoryCount':17,'indexDigest':'sha256:'+r.helpers.file_hash(root/'catalog.canonical.json'),'entries':{slug:'sha256:'+r.helpers.file_hash(root/'entries'/ (slug+'.json')) for slug in slugs}}
        write('release-manifest.json',manifest)
        report={'applications':21,'repositories':17,'files':{str(p.relative_to(root)):{'bytes':p.stat().st_size,'sha256':r.helpers.file_hash(p)} for p in root.rglob('*') if p.is_file()}}
        log=parent/'log';log.write_text(json.dumps(report));return root,log
    def test_complete_twelve_consumer_events(self):self.assertEqual(self.verify(self.events())['named_pass'],12)
    def test_missing_duplicate_unknown_run_reject(self):
        for kind in ('missing','duplicate','unknown'):
            rows=self.events()
            if kind=='missing':rows.pop(0)
            elif kind=='duplicate':rows.insert(0,copy.deepcopy(rows[0]))
            else:rows[0]['Test']='unknown'
            with self.subTest(kind=kind),self.assertRaises(AssertionError):self.verify(rows)
    def test_failed_skipped_or_wrong_package_reject(self):
        for kind in ('fail','skip','package'):
            rows=self.events()
            if kind=='package':rows[0]['Package']='other'
            else:rows[1]['Action']=kind
            with self.subTest(kind=kind),self.assertRaises(AssertionError):self.verify(rows)
    def test_global_build_or_race_fault_reject(self):
        for row in ({'Action':'build-fail','Package':r.PACKAGE},{'Action':'output','Output':'DATA RACE','Package':r.PACKAGE}):
            with self.subTest(row=row),self.assertRaises(AssertionError):self.verify(self.events()+[row])
    def test_complete_report_graph(self):
        with tempfile.TemporaryDirectory() as directory:
            root,log=self.fixture(Path(directory));self.assertEqual(r.verify_producer(log,root)['applications'],21)
    def test_unreported_changed_symlink_or_wrong_mode_reject(self):
        for kind in ('extra','changed','symlink','mode'):
            with tempfile.TemporaryDirectory() as directory:
                root,log=self.fixture(Path(directory));target=root/'entries/fixture-0.json'
                if kind=='extra':(root/'extra').write_text('unreported')
                elif kind=='changed':target.write_text('changed')
                elif kind=='symlink':target.unlink();target.symlink_to(root/'catalog.canonical.json')
                else:target.chmod(0o644)
                with self.subTest(kind=kind),self.assertRaises(AssertionError):r.verify_producer(log,root)
if __name__=='__main__':unittest.main()
