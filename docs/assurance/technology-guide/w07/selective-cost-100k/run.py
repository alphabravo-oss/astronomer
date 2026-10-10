import subprocess,json,pathlib,os,urllib.parse,time,hashlib,sys
repo=pathlib.Path('/root/astronomer-all/astronomer-032-w02-current');out=pathlib.Path('/tmp/w07-cost-3c405b96');major=int(sys.argv[1]); prefix='postgres'+str(major)
assert subprocess.check_output(['git','status','--porcelain'],cwd=repo)==b''
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo).decode().strip().startswith('3c405b96')
def guard(tag):
 p=subprocess.run(['bash','scripts/check-build-capacity.sh','--json'],cwd=repo,capture_output=True,text=True)
 with (out/(prefix+'-capacity.jsonl')).open('a') as f:f.write(json.dumps({'stage':tag,'time':time.time(),'exit':p.returncode,'stdout':p.stdout,'stderr':p.stderr})+'\n')
 assert p.returncode==0,'fatal capacity guard'
guard('preflight')
k=['/root/.cache/astronomer-032/toolchains/bin/kubectl-1.37.1','--kubeconfig','/root/.cache/astronomer-032/qualification/cnpg36/kubeconfig','-n','plan032-recovery']
def get(kind,name=None):return json.loads(subprocess.check_output(k+['get',kind]+([name] if name else [])+['-o','json'],stderr=subprocess.DEVNULL))
cluster='plan032-postgres'+('18' if major==18 else '');uid={17:'2f83aaff-dbbf-40ef-8880-542225fec0b4',18:'ea0f410a-e1ea-4cb4-9fe5-40a35a020d3e'}[major]
c=get('cluster',cluster);assert c['metadata']['uid']==uid and c['metadata']['labels'].get('astronomer.io/owner')=='plan032-20261009'
pod=get('pod',c['status']['currentPrimary']);assert pod['metadata']['labels'].get('cnpg.io/cluster')==cluster
source=pathlib.Path('/root/.cache/astronomer-032/qualification/cnpg36/w06-category-database.json') if major==17 else pathlib.Path('/tmp/w07-cnpg/database18.json')
u=urllib.parse.urlparse(json.loads(source.read_text())['database_url'])._replace(path='/plan032_w07_cursor'+str(major));url=urllib.parse.urlunparse(u)
assert u.port=={17:30432,18:30433}[major]
services=get('services')['items'];matches=[s for s in services if any(p.get('nodePort')==u.port for p in s['spec'].get('ports',[]))];assert len(matches)==1;s=matches[0]
assert s['spec']['selector']=={'cnpg.io/cluster':cluster,'cnpg.io/instanceRole':'primary'}
e=get('endpoints',s['metadata']['name']);addresses=[a for sub in e.get('subsets',[]) for a in sub.get('addresses',[])];assert len(addresses)==1
assert addresses[0]['targetRef']['uid']==pod['metadata']['uid'] and addresses[0]['targetRef']['name']==pod['metadata']['name']
node=get('node',addresses[0]['nodeName']);assert u.hostname in [a['address'] for a in node['status']['addresses']],'node host mismatch'
private=pathlib.Path('/root/.cache/astronomer-032/qualification/w07-cost-3c405b96');private.mkdir(mode=0o700,exist_ok=True);ref=private/(prefix+'-database.json');ref.write_text(json.dumps({'database_url':url}));ref.chmod(0o600)
env=dict(os.environ,PGHOST=u.hostname,PGPORT=str(u.port),PGDATABASE=u.path.lstrip('/'),PGUSER=urllib.parse.unquote(u.username),PGPASSWORD=urllib.parse.unquote(u.password))
for key,value in urllib.parse.parse_qsl(u.query):
 if key=='sslmode':env['PGSSLMODE']=value
p=subprocess.run(['psql','-X','-At','-v','ON_ERROR_STOP=1','-c',"SELECT current_setting('server_version_num')::int/10000,current_database(),version,dirty FROM schema_migrations"],env=env,capture_output=True,text=True)
assert p.returncode==0,'private connector failure';assert p.stdout.strip().split('|')==[str(major),'plan032_w07_cursor'+str(major),'75','f'],'connected identity mismatch'
fence={'cluster':cluster,'cluster_uid':uid,'primary':pod['metadata']['name'],'pod_uid':pod['metadata']['uid'],'service':s['metadata']['name'],'service_uid':s['metadata']['uid'],'selector':s['spec']['selector'],'endpoint_uid':addresses[0]['targetRef']['uid'],'node_host_verified':True,'server_major':major,'schema':75,'dirty':False,'private_reference':str(ref)}
(out/(prefix+'-fences.json')).write_text(json.dumps(fence,indent=2)+'\n')
print(json.dumps(fence),flush=True)
guard('before-go')
env=dict(os.environ,PAGINATION_TEST_DATABASE_URL=url,GOMAXPROCS='4',GOFLAGS='-p=2',GOWORK='off')
argv=['/usr/local/go/bin/go','test','-count=1','-json','-run','^TestControlPlaneCursorPostgresRepresentativeSelectiveCost$','./internal/handler']
start=time.time();log=out/(prefix+'-test.jsonl')
with log.open('w') as f:
 p=subprocess.Popen(argv,cwd=repo,env=env,stdout=f,stderr=subprocess.STDOUT)
 while p.poll() is None:
  time.sleep(10)
  try:guard('maintained')
  except AssertionError:
   p.terminate();p.wait();raise
receipt={'source':subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo).decode().strip(),'argv':argv,'major':major,'exit':p.returncode,'elapsed_seconds':time.time()-start,'log_sha256':hashlib.sha256(log.read_bytes()).hexdigest(),'source_unchanged':subprocess.check_output(['git','status','--porcelain'],cwd=repo)==b''}
(out/(prefix+'-receipt.json')).write_text(json.dumps(receipt,indent=2)+'\n');guard('after-go');print(json.dumps(receipt),flush=True)
sys.exit(p.returncode)
