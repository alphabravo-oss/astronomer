import datetime,hashlib,json,os,pathlib,signal,subprocess,time
root=pathlib.Path.cwd();out=pathlib.Path('/tmp/w02-pagination-after-c89b8c13');source=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip();assert source=='c89b8c134ae719a5b496e11095084ed1b10c50ac';assert not subprocess.check_output(['git','status','--porcelain'],text=True)
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
guards=[];stages=[]
def guard():
    p=subprocess.run(['scripts/check-build-capacity.sh','--path','.','--json'],capture_output=True,text=True);value=json.loads(p.stdout);value['at']=now();guards.append(value)
    if p.returncode or not value['meets_minimum']:raise RuntimeError('capacity guard failed')
    return value
regex='^(TestLegacySQLPaginationParserCharacterization|TestLegacySQLPaginationDefaultsCharacterization|TestToolSQLWindowMatchesWireMetadata|TestTypedSQLPaginationMatchesLegacyBounds)$'
commands=[('focused-race',['/usr/local/go/bin/go','test','-race','-json','-count=1','-timeout=2m','./internal/handler','-run',regex]),('handler-vet',['/usr/local/go/bin/go','vet','./internal/handler']),('handler-full-race',['/usr/local/go/bin/go','test','-race','-json','-count=1','./internal/handler'])]
env=os.environ.copy();env.update(GOMAXPROCS='4',GOFLAGS='-p=2',GOWORK='off')
assert not env.get('TABLE_VIEWS_TEST_DATABASE_URL') and not env.get('PAGINATION_TEST_DATABASE_URL'), 'unallocated live fixture environment'
paths=['go.mod', 'go.sum', 'internal/handler/alerting_channels.go', 'internal/handler/alerting_events.go', 'internal/handler/alerting_inhibitions.go', 'internal/handler/alerting_rules.go', 'internal/handler/alerting_silences.go', 'internal/handler/apiserver_audit.go', 'internal/handler/audit.go', 'internal/handler/auth_tokens.go', 'internal/handler/backups_restores.go', 'internal/handler/backups_runs.go', 'internal/handler/backups_schedules.go', 'internal/handler/backups_storage.go', 'internal/handler/catalog_installations.go', 'internal/handler/catalog_installed_enriched.go', 'internal/handler/catalog_repositories.go', 'internal/handler/chart_ratings.go', 'internal/handler/cluster_agents_inventory.go', 'internal/handler/cluster_agents_upgrade.go', 'internal/handler/cluster_templates_registry.go', 'internal/handler/image_vulns.go', 'internal/handler/image_vulns_history.go', 'internal/handler/logging.go', 'internal/handler/logging_outputs.go', 'internal/handler/logging_pipelines.go', 'internal/handler/monitoring_operations.go', 'internal/handler/native_rbac.go', 'internal/handler/network_policies_list.go', 'internal/handler/pagination_sql_characterization_test.go', 'internal/handler/pagination_sql_typed_test.go', 'internal/handler/project_catalogs.go', 'internal/handler/project_navigation.go', 'internal/handler/projects_crud.go', 'internal/handler/rbac_bindings.go', 'internal/handler/rbac_roles.go', 'internal/handler/resources_sql_lists.go', 'internal/handler/response.go', 'internal/handler/security_policies.go', 'internal/handler/security_scans.go', 'internal/handler/security_templates.go', 'internal/handler/tools_catalog.go', 'internal/pagination/page.go', 'internal/handler/pagination_request.go'];inputs={p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in paths}
def save():
    value=dict(schema_version=1,source_commit=source,source_clean_before=True,source_unchanged_after=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()==source and not subprocess.check_output(['git','status','--porcelain'],text=True),source_inputs=inputs,environment={'GOMAXPROCS':'4','GOFLAGS':'-p=2','GOWORK':'off'},stages=stages,capacity_guards=guards)
    (out/'receipt.json').write_text(json.dumps(value,indent=2)+'\n')
for name,argv in commands:
    try:guard()
    except RuntimeError:
        stages.append(dict(name=name,argv=argv,status='NOT_RUN_CAPACITY'));save();break
    started=now();held=None;path=out/(name+'.log')
    with path.open('w') as log:
        os.chmod(path,0o600);process=subprocess.Popen(argv,stdout=log,stderr=subprocess.STDOUT,env=env,start_new_session=True)
        while process.poll() is None:
            time.sleep(5)
            try:guard()
            except RuntimeError as err:
                held=str(err);os.killpg(process.pid,signal.SIGTERM);break
        code=process.wait()
        if held:
            try:os.killpg(process.pid,signal.SIGKILL)
            except ProcessLookupError:pass
    events=[]
    for line in path.read_text().splitlines():
        try:events.append(json.loads(line))
        except json.JSONDecodeError:pass
    endings={e['Test']:e['Action'] for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')};parents={name:action for name,action in endings.items() if '/' not in name};leaves={name:action for name,action in endings.items() if not any(other.startswith(name+'/') for other in endings)}
    stage=dict(name=name,argv=argv,started_at=started,closed_at=now(),exit_code=code,capacity_stop=held,parents=parents,leaf_cases=leaves,event_results=endings,artifact=path.name,artifact_sha256=hashlib.sha256(path.read_bytes()).hexdigest());stages.append(stage);save();print(json.dumps({'stage':name,'exit_code':code,'parent_count':len(parents),'leaf_count':len(leaves),'event_count':len(endings),'failed':[name for name,action in endings.items() if action=='fail'],'skipped':[name for name,action in endings.items() if action=='skip'],'capacity_stop':held}),flush=True)
    if code or held:break
