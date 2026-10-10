import datetime,hashlib,json,os,pathlib,signal,subprocess,time
root=pathlib.Path.cwd();out=pathlib.Path('/tmp/w07-supportbundle-baseline-df5598e2');source=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip();assert source=='df5598e2c636e1ee21dfa102d4bbbe7ee3046b8f';assert not subprocess.check_output(['git','status','--porcelain'],text=True)
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def guard():
    p=subprocess.run(['scripts/check-build-capacity.sh','--path','.','--json'],capture_output=True,text=True)
    value=json.loads(p.stdout);value['at']=now();guards.append(value)
    if p.returncode or not value['meets_minimum']:raise RuntimeError('capacity guard failed')
    return value
guards=[];guard();argv=['/usr/local/go/bin/go','test','-json','-count=1','-timeout=2m','./internal/handler','-run','^(TestSupportBundleKubernetesContinuesNamespaceLists|TestSupportBundleTLSSecretBeyondFirstPageIsPresent|TestSupportBundleExpiredSecretContinuationNeverClaimsAbsent)$'];started=now();env=os.environ.copy();env.update(GOMAXPROCS='4',GOFLAGS='-p=2',GOWORK='off');held=None
with (out/'go-test.jsonl').open('w') as log:
    os.chmod(out/'go-test.jsonl',0o600);process=subprocess.Popen(argv,stdout=log,stderr=subprocess.STDOUT,env=env,start_new_session=True)
    while process.poll() is None:
        time.sleep(5)
        try:guard()
        except RuntimeError as err:
            held=str(err);os.killpg(process.pid,signal.SIGTERM);break
    exitcode=process.wait()
    if held:
        try:os.killpg(process.pid,signal.SIGKILL)
        except ProcessLookupError:pass
closed=now();events=[]
for line in (out/'go-test.jsonl').read_text().splitlines():
    try:events.append(json.loads(line))
    except json.JSONDecodeError:pass
ended=[e for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')];tests={e['Test']:e['Action'] for e in ended};parents={name:action for name,action in tests.items() if '/' not in name};leaves={name:action for name,action in tests.items() if not any(other.startswith(name+'/') for other in tests)}
inputs=['internal/handler/supportbundle_kubernetes.go','internal/handler/supportbundle_continuation_test.go','internal/handler/supportbundle_redaction.go','internal/handler/compliance_test.go','internal/handler/supportbundle_test.go','go.mod','go.sum']
receipt=dict(schema_version=1,source_commit=source,source_clean_before=True,source_unchanged_after=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()==source and not subprocess.check_output(['git','status','--porcelain'],text=True),started_at=started,closed_at=closed,argv=argv,environment={'GOMAXPROCS':'4','GOFLAGS':'-p=2','GOWORK':'off'},exit_code=exitcode,capacity_stop=held,parents=parents,leaf_cases=leaves,expected_parents=3,expected_leaf_cases=5,source_inputs={p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in inputs},artifacts={'go-test.jsonl':hashlib.sha256((out/'go-test.jsonl').read_bytes()).hexdigest()},capacity_guards=guards)
(out/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps({'exit_code':exitcode,'parents':parents,'leaf_cases':leaves,'capacity_stop':held,'source_unchanged':receipt['source_unchanged_after'],'closed_at':closed},indent=2))
