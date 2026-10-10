"""Finite focused mesh AFTER using the reviewed held-leader runner."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

ROOT=Path('/root/astronomer-all/astronomer-032-w07-mesh-before')
SOURCE='4f52252f7e215562c582be134b0d8de62c695c20'
REVIEWED=Path('/tmp/w07-after-query-ee2c52d8/run_after.py')
assert hashlib.sha256(REVIEWED.read_bytes()).hexdigest()=='4b1037ee327b96de13338c5ab8ba1366e5a6bec007f0c8cf634faf50dfaace46'
spec=importlib.util.spec_from_file_location('reviewed_after',REVIEWED);after=importlib.util.module_from_spec(spec);spec.loader.exec_module(after)
after.ROOT=ROOT
DIRECTORY=Path(__file__).resolve().parent
FINGERPRINT={'schema_version': 2, 'source_file_count': 9089, 'source_tree_sha256': '29b5ece8830dac7e874a959df84d58d1ec09da2f66a7ca55cf4527d2dbd8e74b'}
NODE='/root/.cache/astronomer-032/toolchains/node-v26.11.1-linux-x64/bin/node'
PARENTS = tuple(json.loads((DIRECTORY/'expected.json').read_text())['parents'])
EXPECTED = set(json.loads((DIRECTORY/'expected.json').read_text())['terminals'])
PACKAGE='github.com/alphabravocompany/astronomer-go/internal/handler'
assert len(PARENTS)==22 and len(EXPECTED)==45

def verify_after(log):
    rows=[]
    for line in log.read_text().splitlines():
        row=json.loads(line)
        assert isinstance(row,dict) and row.get('Action') in {'start','run','pause','cont','output','pass','fail','skip','build-output','build-fail'},'non-event output'
        assert row.get('Package')==PACKAGE,'event package drift'
        assert not row.get('Test') or row['Test'] in EXPECTED,'unknown child output'
        assert row['Action']!='build-fail','build failure cannot qualify'
        message=row.get('Output','')
        assert isinstance(message,str) and not any(marker in message for marker in ('DATA RACE','panic:','setup failure','cleanup failure','test timed out')),'runtime/setup/cleanup failure'
        rows.append(row)
    runs=[row['Test'] for row in rows if row['Action']=='run']
    assert len(runs)==len(EXPECTED) and set(runs)==EXPECTED,'exact unique run inventory drift'
    terminals=[row for row in rows if row['Action'] in ('pass','fail','skip') and row.get('Test')]
    assert len(terminals)==len(EXPECTED) and {row['Test'] for row in terminals}==EXPECTED,'named terminal inventory drift'
    assert all(row['Action']=='pass' for row in terminals),'all focused tests must pass without skips'
    packages=[row for row in rows if row['Action'] in ('pass','fail','skip') and not row.get('Test')]
    assert len(packages)==1 and packages[0]['Action']=='pass','package terminal drift'
    return {'named_pass':45,'parent_pass':22,'child_pass':23,'skips':0,'package_pass':1}

def check_bindings():
    path=DIRECTORY/'source-bindings.json'
    assert hashlib.sha256(path.read_bytes()).hexdigest()==BINDINGS_SHA
    for row in json.loads(path.read_bytes()):
        actual=Path(row['path']);assert hashlib.sha256(actual.read_bytes()).hexdigest()==row['sha256']
        if 'inode' in row:
            stat=actual.stat();assert (stat.st_dev,stat.st_ino,stat.st_size,stat.st_mtime_ns)==(row['device'],row['inode'],row['size'],row['mtime_ns'])

def verify_guard(output,exit_code):
    assert exit_code==0 and isinstance(output,str) and len(output)<=4096,'fatal capacity guard'
    row=json.loads(output)
    assert isinstance(row,dict),'malformed capacity object'
    fields=('total_kib','used_kib','available_kib','available_gib','free_percent','minimum_free_gib','minimum_free_percent')
    assert all(type(row.get(key)) is int and 0<=row[key]<(1<<63) for key in fields),'nonfinite/noninteger capacity field'
    assert row['minimum_free_gib']==20 and row['minimum_free_percent']==20,'default guard thresholds changed'
    assert row.get('meets_minimum') is True,'capacity minimum not met'
    assert row['total_kib']>0 and row['used_kib']<=row['total_kib'] and row['available_kib']<=row['total_kib'],'inconsistent capacity bounds'
    assert row['available_gib']==row['available_kib']//(1024*1024),'inconsistent available GiB'
    assert row['available_kib']>=20*1024*1024 and 20<=row['free_percent']<=100,'insufficient default headroom'
    return row

GO_IDENTITY={'path': '/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.2.linux-amd64/bin/go', 'sha256': '548d3c32f83fa81152f543eb1526be7884cfe0526eb5e9ce29d3eca14786e7e0', 'device': 2049, 'inode': 2360704, 'size': 17164055, 'mtime_ns': 1791570925918518586}
GO=Path(GO_IDENTITY['path'])
def go_identity():
    assert not GO.is_symlink(), 'selected Go binary symlink drift'
    stat=GO.stat()
    return {'path':str(GO),'sha256':hashlib.sha256(GO.read_bytes()).hexdigest(),'device':stat.st_dev,'inode':stat.st_ino,'size':stat.st_size,'mtime_ns':stat.st_mtime_ns}

def assert_go_identity():
    assert go_identity()==GO_IDENTITY, 'selected Go binary identity drift'

def interrupt(_number,_frame):
    for number in (signal.SIGTERM,signal.SIGINT):signal.signal(number,signal.SIG_IGN)
    raise RuntimeError('owned lane interrupted')

def step_timeout(name,deadline):
    remaining=deadline-time.monotonic()
    assert remaining>0, 'overall owned lane deadline'
    return min(30 if name=='go-version' else 600 if name in ('focused-race','vet') else 90,remaining)

def main():
    assert len(sys.argv)==2
    output=Path(sys.argv[1]);os.umask(0o077)
    assert not output.resolve().is_relative_to(ROOT.resolve()),'receipt must be outside source'
    output.mkdir(mode=0o700,parents=True,exist_ok=False)
    deadline=time.monotonic()+1500
    source=subprocess.check_output(['git','rev-parse',SOURCE],cwd=ROOT,timeout=10).decode().strip()
    receipt={'source':source,'steps':[],'scope':'Focused 22 mesh/shared collector parents, 45 all-PASS terminals; scoped tunnel and actual owned support HTTP peers, no full handler suite or live Kubernetes/database claim; cached whole-source SQL failures remain unaccepted'}
    def guard(stage):
        check=subprocess.run(['bash','scripts/check-build-capacity.sh','--min-free-gib','20','--min-free-percent','20','--json'],cwd=ROOT,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,timeout=15)
        with (output/'capacity.jsonl').open('a') as stream:stream.write(json.dumps({'stage':stage,'time':time.time(),'exit':check.returncode,'stdout':check.stdout,'stderr':check.stderr})+'\n')
        verify_guard(check.stdout,check.returncode)
    def ownership():
        check_bindings();assert_go_identity()
        assert subprocess.check_output(['git','status','--porcelain'],cwd=ROOT,timeout=10)==b''
        assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,timeout=10).decode().strip()==source
    handlers={number:signal.signal(number,interrupt) for number in (signal.SIGTERM,signal.SIGINT)}
    try:
        guard('initial');ownership();receipt['go_before']=go_identity();receipt['source_before']=after.fences.fingerprint(ROOT)
        assert receipt['source_before']==FINGERPRINT,'frozen source fingerprint drift'
        env=dict(os.environ,GOTOOLCHAIN='go1.27.2',GOWORK='off',GOMAXPROCS='4',GOFLAGS='-p=2 -mod=readonly',GOFIPS140='off')
        env['PATH']=str(GO.parent)+':'+env['PATH']
        receipt['selected_environment']={key:env[key] for key in ('GOTOOLCHAIN','GOWORK','GOMAXPROCS','GOFLAGS','GOFIPS140')}
        wrapper=DIRECTORY/'check_cached.py'
        commands=[('go-version',[str(GO),'version']),('focused-race',[str(GO),'test','-race','-json','-count=1','-run','^('+'|'.join(PARENTS)+')$','./internal/handler']),('vet',[str(GO),'vet','./internal/handler']),('boundaries',[NODE,'scripts/check-dependency-boundaries.mjs']),('complexity',[NODE,'scripts/check-complexity-budget.mjs']),('json-governance',[sys.executable,'-B',str(wrapper),'json',str(ROOT),str(output/'actual-json.json')]),('sql-governance',[sys.executable,'-B',str(wrapper),'sql',str(ROOT),str(output/'actual-sql.json')])]
        for name,command in commands:
            step=after.run_step(command,command,env,output/(name+'.log'),guard,ownership,lambda:after.fences.fingerprint(ROOT),timeout=step_timeout(name,deadline))
            receipt['steps'].append({'name':name,**step})
            result=step.get('outcome') or {}
            assert step['source_stable'] and step['final_guard_pass'] and result.get('drained') and result.get('leaderHeldUntilDrain') and not result.get('guardException') and not result.get('reason'),'ownership/source/guard fault'
            if name=='go-version':
                assert after.passed(step) and (output/'go-version.log').read_text().strip()=='go version go1.27.2 linux/amd64'
            elif name=='sql-governance':
                assert result['exitCode'] in (0,1),'SQL inventory process failure'
                receipt['coupled_sql']={'status':'PASS' if result['exitCode']==0 else 'FAIL','accepted':result['exitCode']==0}
                path=output/'actual-sql.json'
                assert path.is_file() and not path.is_symlink() and path.stat().st_size<=8<<20
                receipt['coupled_sql']['inventory_sha256']=hashlib.sha256(path.read_bytes()).hexdigest()
            else:
                assert after.passed(step),'after gate failure'
                if name=='focused-race':receipt['events']=verify_after(output/'focused-race.log')
    except BaseException as error:receipt['failure_category']=type(error).__name__
    finally:
        try:
            receipt['go_after']=go_identity()
            assert receipt['go_after']==GO_IDENTITY and receipt.get('go_before')==GO_IDENTITY, 'selected Go binary identity drift'
            receipt['source_after']=after.fences.fingerprint(ROOT)
            receipt['source_stable']=receipt.get('source_before')==receipt['source_after']
            guard('runner-final');receipt['runner_final_guard_pass']=True
        except BaseException as error:receipt['final_failure_category']=type(error).__name__
        (output/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')
        for number,handler in handlers.items():signal.signal(number,handler)
    qualified=len(receipt['steps'])==7 and receipt.get('coupled_sql',{}).get('status')=='PASS' and 'events' in receipt and receipt.get('source_stable') and receipt.get('runner_final_guard_pass') and 'failure_category' not in receipt and 'final_failure_category' not in receipt
    print(json.dumps({'receipt':str(output/'receipt.json'),'qualified':bool(qualified)}))
    return 0 if qualified else 1

BINDINGS_SHA='95eae6295c36bfc7b018e2b72aedf5c3ba988a02232d5438fa94463c968447d7'
if __name__=='__main__':raise SystemExit(main())
