"""Finite canonical Python producer and real signed OCI consumer qualification."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

DIRECTORY=Path(__file__).resolve().parent
CATALOG_ROOT=Path('/root/astronomer-all/astronomer-catalog-032-w15')
CATALOG_SOURCE='afd3af678a0c7ab70a1d03b526b6b1b1f6a96135'
GO_ROOT=Path('/root/astronomer-all/astronomer-032-w15-consumer')
GO_SOURCE='b82a6bdd254011ed1323b8bb904a91d38e0346b8'
ENVIRONMENT_SOURCE='4e6d27a1fd2b8320864161017d2c59e077692311'
QUALIFIED=Path('/tmp/w15-catalog-toolchain-qualifier-4e6d27a/run_qualify.py')
assert hashlib.sha256(QUALIFIED.read_bytes()).hexdigest()=='07c0bafe5669efcb2d15308a54d88f15d0515c921cbc98923ca00380f612f7f5'
spec=importlib.util.spec_from_file_location('qualified62',QUALIFIED);q=importlib.util.module_from_spec(spec);spec.loader.exec_module(q)
helpers=q.helpers
HELD=Path('/tmp/w07-service-mesh-before-runner-1f4b07fc/run_before.py')
assert hashlib.sha256(HELD.read_bytes()).hexdigest()=='6d75f483ab40c2efb29b67d4babaff7ad33447bbd9bacac021573a4901148435'
spec=importlib.util.spec_from_file_location('owned_go',HELD);g=importlib.util.module_from_spec(spec);spec.loader.exec_module(g)
after=g.after
PACKAGE='github.com/alphabravocompany/astronomer-go/internal/catalog'
PARENT='TestApplicationCatalogActualProducerSignedOCIConsumer'
CASES=('valid','wrong_key','missing_signature','bad_signature','wrong_signed_subject','changed_manifest','changed_layer','wrong_document_digest','unsupported_schema','unsupported_reader','canceled')
EXPECTED={PARENT}|{PARENT+'/'+name for name in CASES}


def verify_consumer(log):
    rows=[json.loads(line) for line in log.read_text().splitlines()]
    assert rows and all(isinstance(row,dict) and row.get('Package')==PACKAGE and row.get('Action') in ('start','run','pause','cont','output','pass','fail','skip','build-output','build-fail') for row in rows)
    for row in rows:
        assert row['Action']!='build-fail'
        assert not row.get('Test') or row['Test'] in EXPECTED
        text=row.get('Output','')
        assert isinstance(text,str) and not any(marker in text for marker in ('DATA RACE','panic:','test timed out','setup failure','cleanup failure'))
    runs=[row['Test'] for row in rows if row['Action']=='run']
    assert len(runs)==12 and set(runs)==EXPECTED
    terminals=[row for row in rows if row['Action'] in ('pass','fail','skip') and row.get('Test')]
    assert len(terminals)==12 and {row['Test'] for row in terminals}==EXPECTED and all(row['Action']=='pass' for row in terminals)
    assert [row['Action'] for row in rows if row['Action'] in ('pass','fail','skip') and not row.get('Test')]==['pass']
    return {'parents_pass':1,'children_pass':11,'named_pass':12,'package_pass':1,'skips':0}


def verify_producer(log,root):
    report=json.loads(log.read_text())
    assert set(report)=={'files','applications','repositories'}
    assert report['applications']==21 and report['repositories']==17
    files=report['files'];assert isinstance(files,dict) and 0<len(files)<=256
    actual={}
    total=0
    for path in root.rglob('*'):
        assert not path.is_symlink()
        if path.is_dir():
            assert path.stat().st_mode & 0o777==0o700
            continue
        assert path.is_file() and path.stat().st_nlink==1 and path.stat().st_mode & 0o777==0o600
        relative=str(path.relative_to(root));size=path.stat().st_size
        assert 0<size<=4<<20
        actual[relative]={'bytes':size,'sha256':helpers.file_hash(path)};total+=size
    assert actual==files and total<=64<<20
    for name,row in files.items():
        path=Path(name)
        assert not path.is_absolute() and '..' not in path.parts and len(name)<=256
        assert set(row)=={'bytes','sha256'} and type(row['bytes']) is int and re.fullmatch('[0-9a-f]{64}',row['sha256'])
    catalog=helpers.read_json(root/'catalog.canonical.json');manifest=helpers.read_json(root/'release-manifest.json')
    assert catalog['metadata']['schemaVersion']==catalog['metadata']['minimumReaderVersion']==1
    assert len(catalog['applications'])==report['applications'] and len(catalog['repositories'])==report['repositories']
    assert manifest['applicationCount']==report['applications'] and manifest['repositoryCount']==report['repositories']
    assert manifest['indexDigest']=='sha256:'+files['catalog.canonical.json']['sha256']
    entries={row['slug'] for row in catalog['applications']};assert len(entries)==21 and 'constellation' in entries and set(manifest['entries'])==entries
    for slug in entries:
        assert manifest['entries'][slug]=='sha256:'+files['entries/'+slug+'.json']['sha256']
    return report


def check_bindings():
    assert helpers.file_hash(DIRECTORY/'source-bindings.json')==BINDINGS_SHA
    for row in helpers.read_json(DIRECTORY/'source-bindings.json'):
        path=Path(row['path']);assert helpers.file_hash(path)==row['sha256']
        if 'bytes' in row:assert path.stat().st_size==row['bytes']
        if 'inode' in row:
            s=path.stat();assert (s.st_dev,s.st_ino,s.st_size,s.st_mtime_ns)==(row['device'],row['inode'],row['size'],row['mtime_ns'])
    for name,digest in LOCAL_INPUTS.items():assert helpers.file_hash(DIRECTORY/name)==digest


def fingerprints():
    return {'catalog':q.build.fingerprint(),'consumer':after.fences.fingerprint(GO_ROOT)}


def main():
    assert len(sys.argv)==2
    os.umask(0o077);output=helpers.private_directory(Path(sys.argv[1]),create=True)
    assert not any(output.resolve().is_relative_to(root.resolve()) for root in (CATALOG_ROOT,GO_ROOT))
    deadline=time.monotonic()+900
    receipt={'catalog_source':CATALOG_SOURCE,'consumer_source':GO_SOURCE,'environment_source':ENVIRONMENT_SOURCE,'scope':'Two canonical private producer outputs with complete deterministic file graph; actual signed synthetic-key OCI consumer and zero-write negatives. No registry TLS, keyless trust, GitHub publication, database or customer deployment.','steps':[],'qualified':False}
    environment=None
    def ownership():
        check_bindings();g.assert_go_identity()
        for root,head in ((CATALOG_ROOT,CATALOG_SOURCE),(GO_ROOT,GO_SOURCE)):
            assert subprocess.check_output(['git','status','--porcelain'],cwd=root,timeout=10)==b''
            assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True,timeout=10).strip()==head
        if environment is not None:
            assert helpers.tree_binding(Path(environment['tools_root']))==environment['tools_binding']
            for prefix in ('','original_'):
                assert helpers.environment_binding(Path(environment[prefix+'environment_root']),Path(environment['python']))==environment[prefix+'environment_binding']
    def guard(stage):
        result=subprocess.run(['bash','scripts/check-build-capacity.sh','--min-free-gib','20','--min-free-percent','20','--json'],cwd=GO_ROOT,capture_output=True,text=True,timeout=15)
        with (output/'capacity.jsonl').open('a') as stream:stream.write(json.dumps({'stage':stage,'time':time.time(),'exit':result.returncode,'stdout':result.stdout,'stderr':result.stderr})+'\n')
        g.verify_guard(result.stdout,result.returncode)
    previous={number:signal.signal(number,g.interrupt) for number in (signal.SIGINT,signal.SIGTERM)}
    try:
        guard('initial');ownership();receipt['source_before']=fingerprints();assert receipt['source_before']==FINGERPRINTS
        environment=q.current_environment();assert q.SOURCE==ENVIRONMENT_SOURCE
        receipt['environment_binding']=environment['environment_binding'];receipt['private_environment_receipt_sha256']=q.ENVIRONMENT_SHA
        pyenv=q.build.selected_environment(output)
        for name in ('home','home/config','cache'):(output/name).mkdir(mode=0o700)
        goenv=dict(os.environ,GOTOOLCHAIN='go1.27.2',GOWORK='off',GOMAXPROCS='4',GOFLAGS='-p=2 -mod=readonly',GOFIPS140='off');goenv['PATH']=str(g.GO.parent)+':'+goenv['PATH']
        receipt['safe_go_environment']={key:goenv[key] for key in ('GOTOOLCHAIN','GOWORK','GOMAXPROCS','GOFLAGS','GOFIPS140')}
        python=str(Path(environment['environment_root'])/'bin/python')
        def step(name,command,env,root,cap):
            remaining=deadline-time.monotonic();assert remaining>0
            after.ROOT=root
            row=after.run_step(command,command,env,output/(name+'.log'),guard,ownership,fingerprints,timeout=min(cap,remaining));receipt['steps'].append({'name':name,**row})
            assert after.passed(row) and row['outcome']['leaderHeldUntilDrain'] and not row['outcome']['guardException'] and not row['outcome']['liveDescendantsAfterDrain'],name+' failed'
        step('python-version',[python,'--version'],pyenv,CATALOG_ROOT,30);assert (output/'python-version.log').read_text().strip()=='Python 3.15.0'
        step('go-version',[str(g.GO),'version'],goenv,GO_ROOT,30);assert (output/'go-version.log').read_text().strip()=='go version go1.27.2 linux/amd64'
        reports=[]
        for seed in ('0','1'):
            env=dict(pyenv,PYTHONHASHSEED=seed);target=output/('producer-'+seed)
            step('producer-'+seed,[python,'-B',str(DIRECTORY/'produce.py'),str(CATALOG_ROOT),str(target)],env,CATALOG_ROOT,30)
            reports.append(verify_producer(output/('producer-'+seed+'.log'),target))
        assert reports[0]==reports[1],'canonical full output graph is nondeterministic'
        receipt['producer_graph']=reports[0]
        goenv.update(ASTRONOMER_W15_CATALOG_INPUT=str(output/'producer-0/catalog.canonical.json'),ASTRONOMER_W15_CATALOG_DIGEST='sha256:'+reports[0]['files']['catalog.canonical.json']['sha256'])
        step('consumer-race',[str(g.GO),'test','-race','-json','-count=1','-tags','catalogproducerinterop','-run','^'+PARENT+'$','./internal/catalog'],goenv,GO_ROOT,600)
        receipt['consumer_events']=verify_consumer(output/'consumer-race.log');receipt['qualified']=True
    except BaseException as error:
        receipt['failure_category']=type(error).__name__;receipt['failure_message']=str(error) if isinstance(error,AssertionError) else ''
    finally:
        try:
            ownership();receipt['source_after']=fingerprints();receipt['source_stable']=receipt.get('source_before')==receipt['source_after']
        except BaseException as error:receipt['final_failure_category']=type(error).__name__
        try:guard('runner-final');receipt['runner_final_guard_pass']=True
        except BaseException as error:receipt['final_guard_failure_category']=type(error).__name__
        receipt['qualified']=bool(receipt['qualified'] and receipt.get('source_stable') and receipt.get('runner_final_guard_pass') and not any(key.endswith('failure_category') for key in receipt))
        helpers.private_write(output/'receipt.json',(json.dumps(receipt,indent=2)+'\n').encode())
        for number,handler in previous.items():signal.signal(number,handler)
    print(json.dumps({'receipt':str(output/'receipt.json'),'qualified':receipt['qualified']}));return 0 if receipt['qualified'] else 1

BINDINGS_SHA='e2dd482ce617483bc8db01afb66bae0d8afb79b11ef11ef539b6572b5b1e54ba'
LOCAL_INPUTS={'produce.py': 'bd846e6cf1366137adce551e20fb3a3f0ab546c291d84f04e556c67a9bf73298', 'README.md': 'f39420929b21158093cab85fd1016333ad8a9356be75c077940a47e2eb1dd1bf', 'test_controls.py': '5b4e91cfe3e31a71c746b0d5bf6abc5ed1957065d2a20c0331fae7450174ee69', 'current-fingerprints.json': 'df4a833b7acfc225ab2a039919d23e9d58fa0980ad27c2254a4edfe0f30c12cd'}
FINGERPRINTS={'catalog': {'schema_version': 2, 'source_file_count': 75, 'source_tree_sha256': '124ed4b786281116a4e159b21ef74bbb94246bc61dbb61b9679e43187551337d'}, 'consumer': {'schema_version': 2, 'source_file_count': 9087, 'source_tree_sha256': 'd478442c5a9a11798e8911991e47bf3ec9d1554fa2e117b555a45aad4e7ea5c0'}}
if __name__=='__main__':raise SystemExit(main())
