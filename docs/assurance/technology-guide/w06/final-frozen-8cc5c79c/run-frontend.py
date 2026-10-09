import datetime,hashlib,json,os,pathlib,subprocess,time
root=pathlib.Path('/root/astronomer-all/astronomer-032-category-integration')
out=pathlib.Path('/root/.cache/astronomer-032/qualification/w06-combined-8cc5c79c')
env=os.environ.copy();env['PATH']='/root/.cache/astronomer-032/toolchains/npm-runtime/node_modules/.bin:/root/.cache/astronomer-032/toolchains/node-v26.11.1-linux-x64/bin:'+env['PATH'];env.update(GOTOOLCHAIN='go1.27.2',GOMAXPROCS='2',GOFLAGS='-p=2')
def capture(args,cwd=root):return subprocess.check_output(args,cwd=cwd,env=env,text=True).strip()
def digest_tree():return json.loads(capture(['python3','scripts/hash-source-tree.py','--root',str(root)]))
assert capture(['git','rev-parse','HEAD'])=='8cc5c79c82bc8b2f54e92032418d93606e7e6ff1'
assert not capture(['git','status','--porcelain'])
assert capture(['node','--version'])=='v26.11.1'
assert capture(['npm','--version'])=='12.2.0'
source=digest_tree();started=datetime.datetime.now(datetime.timezone.utc).isoformat()
receipt={'schemaVersion':1,'sourceCommit':capture(['git','rev-parse','HEAD']),'sourceTree':capture(['git','rev-parse','HEAD^{tree}']),'sourceHash':source,'startedAt':started,'runtime':{'node':'26.11.1','npm':'12.2.0','playwright':json.loads((root/'frontend/node_modules/@playwright/test/package.json').read_text())['version'],'chromiumExecutable':capture(['node','-e','import("@playwright/test").then(({chromium})=>process.stdout.write(chromium.executablePath()))'],root/'frontend')},'steps':[],'status':'running','limits':['One immutable combined W01/W05/W06/W09 frontend source; no real authenticated server, shipping Safari or human-accessibility certification claimed.','Parallel frozen API/SDK gate is reported separately.']}
ci=out/'npm-ci.log';receipt['strictNpmCI']={'status':'pass','exitCode':0,'logDigest':'sha256:'+hashlib.sha256(ci.read_bytes()).hexdigest()}
steps=[('type-check',['npm','run','type-check']),('lint',['npm','run','lint']),('code-health',['npm','run','code-health']),('full-vitest',['npm','test','--','--maxWorkers=1']),('production-build-csp',['npm','run','build']),('bundle-budget',['npm','run','bundle:check']),('browser-components',['npm','run','test:browser-components','--','--workers=1','--retries=0'])]
def save(): (out/'qualification.json').write_text(json.dumps(receipt,indent=2,sort_keys=True)+'\n')
save()
for name,args in steps:
 begun=time.monotonic();stamp=datetime.datetime.now(datetime.timezone.utc).isoformat();log=out/(name+'.log')
 with log.open('wb') as stream:r=subprocess.run(args,cwd=root/'frontend',env=env,stdout=stream,stderr=subprocess.STDOUT)
 item={'name':name,'command':args,'startedAt':stamp,'durationSeconds':round(time.monotonic()-begun,3),'exitCode':r.returncode,'status':'pass' if r.returncode==0 else 'fail','logDigest':'sha256:'+hashlib.sha256(log.read_bytes()).hexdigest()};receipt['steps'].append(item)
 print(name,item['status'],item['durationSeconds'],flush=True)
 if r.returncode:
  receipt['status']='failed';break
 save()
else:receipt['status']='passed'
receipt['finishedAt']=datetime.datetime.now(datetime.timezone.utc).isoformat();receipt['finishedSourceHash']=digest_tree();receipt['sourceStable']=receipt['finishedSourceHash']==source
if not receipt['sourceStable']:receipt['status']='failed-source-drift'
save()
raise SystemExit(0 if receipt['status']=='passed' else 1)
