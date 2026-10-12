import re,json,hashlib,pathlib,subprocess,collections,argparse,gzip
parser=argparse.ArgumentParser(description='Source-only finite pagination candidate reconciliation; no runtime or authorization qualification.')
parser.add_argument('--repo',type=pathlib.Path,required=True)
parser.add_argument('--inputs',type=pathlib.Path,required=True)
parser.add_argument('--output',type=pathlib.Path,required=True)
args=parser.parse_args()
root=args.repo.resolve();out=args.inputs.resolve()
def input_text(name):
 p=out/name
 return p.read_text() if p.exists() else gzip.decompress((out/(name+'.gz')).read_bytes()).decode()
old=json.loads(input_text('runtime-handler-candidates.json'))
ops=json.loads(input_text('openapi-list-candidates.json'))
sha=lambda s:hashlib.sha256(s.encode()).hexdigest()
def mask(s):
 # Keep token positions while masking comments/string contents; braces within
 # URL placeholders and JSON/SQL literals must not affect lexical ranges.
 pat=r'//[^\n]*|/\*[\s\S]*?\*/|`[^`]*`|"(?:\\.|[^"\\])*"|\'(?:\\.|[^\'\\])*\''
 return re.sub(pat,lambda m:''.join('\n' if c=='\n' else ' ' for c in m[0]),s)
def endbrace(s,start):
 m=mask(s);depth=0
 for i in range(start,len(s)):
  if m[i]=='{':depth+=1
  if m[i]=='}':
   depth-=1
   if depth==0:return i+1
 raise ValueError('unclosed body')
def funcs(path):
 s=path.read_text();result=[]
 for m in re.finditer(r'^func\s+(?:\(([^\n)]*)\)\s+)?(\w+)\([^\n]*',s,re.M):
  start=s.index('{',m.start());end=endbrace(s,start)
  result.append(dict(receiver=m[1],function=m[2],start=m.start(),end=end,body=s[start:end],line=s.count('\n',0,m.start())+1))
 return s,result
# Concrete dependency fields provide type identity; route source lines remain
# evidence, not a claim that lexical tracing proves all authorization paths.
dep=(root/'internal/server/router_dependencies.go').read_text()
fields=dict(re.findall(r'^\s*(\w+)\s+\*(?:handler|deliveryhandler)\.(\w+Handler)',dep,re.M))
routes=[]
for p in sorted((root/'internal/server').glob('*.go')):
 if p.name.endswith('_test.go'):continue
 s=p.read_text();scopes=[]
 for m in re.finditer(r'\.Route\("([^"]*)",\s*func\([^)]*\)\s*{',s):
  start=s.index('{',m.start()+s[m.start():m.end()].index('func'))
  scopes.append((start,endbrace(s,start),m[1]))
 for m in re.finditer(r'\bGet\("([^"]+)"\s*,\s*(deps\.\w+\.\w+\.\w+)',s):
  chain=m[2].split('.');prefix=''.join(v for a,b,v in scopes if a<m.start()<b)
  full='/api/v1'+prefix+m[1]; typ=fields.get(chain[-2]);line=s.count('\n',0,m.start())+1
  # Up to three preceding lines retain split With(permission...) evidence.
  snippet='\n'.join(s.splitlines()[max(0,line-4):line+1]).strip()
  routes.append(dict(path=full.rstrip('/'),source=str(p.relative_to(root)),line=line,module_sha256=sha(s),selector=m[2],handler_type=typ,function=chain[-1],registration_evidence=snippet))
# SQL named queries canonical sources only, retaining all definitions (including
# extension templates); no generated source is treated as canonical authority.
sql={}
for base in ['internal/db/queries','internal/db/queryextensions']:
 for p in (root/base).rglob('*'):
  if not p.is_file() or not (p.name.endswith('.sql') or p.name.endswith('.tmpl')):continue
  s=p.read_text()
  for m in re.finditer(r'-- name:\s*(\w+)\s*:\w+',s):
   nxt=re.search(r'-- name:',s[m.end():]);end=m.end()+nxt.start() if nxt else len(s)
   text=s[m.start():end]
   sql.setdefault(m[1],[]).append(dict(path=str(p.relative_to(root)),line=s.count('\n',0,m.start())+1,module_sha256=sha(s),order_by=re.findall(r'ORDER BY\s+([^;]+)',text,re.I),limits=re.findall(r'\bLIMIT\s+([^;\n]+)',text,re.I),offsets=re.findall(r'\bOFFSET\s+([^;\n]+)',text,re.I)))
# Explicit source-classification overrides cover non-list helpers, internal
# mutation reads, fixed catalogs/projections and independent protocol families.
overrides={47:'existing-cluster-cursor',64:'existing-cluster-cursor',30:'helm-or-delivery-history-delegate',152:'fixed-catalog',165:'kubernetes-collection-delegate',25:'internal-filter-expansion',28:'mutation-internal-collection',33:'signed-cursor-delegate',48:'detail-embedded-collection',51:'mutation-internal-collection',69:'aggregate-projection',73:'cursor-parser-helper',75:'aggregate-render',76:'aggregate-render',77:'aggregate-render',83:'detail-embedded-collection',85:'aggregate-projection',86:'aggregate-projection',87:'bounded-estate-with-sentinel',89:'selected-input-projection',91:'detail-embedded-collection',98:'mutation-internal-collection',99:'mutation-internal-collection',116:'detail-embedded-collection',117:'guarded-parent-delegate',118:'guarded-parent-delegate',128:'detail-embedded-collection',135:'public-settings-projection',136:'external-directory-search',142:'mutation-internal-collection',143:'aggregate-projection',144:'aggregate-projection',175:'detail-embedded-collection',176:'aggregate-projection',187:'detail-embedded-collection',188:'aggregate-projection',1:'aggregate-projection',6:'aggregate-projection',43:'bounded-recommendation',44:'bounded-recommendation',65:'full-scoped-collection',66:'full-scoped-collection',96:'fixed-catalog',45:'fixed-catalog',171:'fixed-catalog',163:'scim-offset-protocol',161:'scim-offset-protocol',37:'charlie-bounded-offset-protocol',39:'charlie-bounded-offset-protocol',41:'charlie-fixed-window',38:'charlie-service-delegate',40:'charlie-service-delegate',36:'charlie-service-delegate',108:'bounded-ranking',109:'bounded-time-series',110:'bounded-time-series',156:'bounded-activity-feed',157:'cross-cluster-search-delegate',102:'kubernetes-collection-delegate',126:'prometheus-protocol-delegate',3:'provider-fixed-first-page',2:'provider-registered-queue-catalog',132:'full-scoped-collection',153:'full-scoped-collection',169:'full-scoped-collection',179:'full-scoped-collection',172:'full-user-scoped-collection'}
notes={
'existing-cluster-cursor':'Existing cluster inventory cursor protocol is distinct from the new signed catalog/control-plane codec. Offset guard alone does not reject cursor; preserve released cluster behavior and trace scoped query/filter delegate.',
'helm-or-delivery-history-delegate':'Release history authorizes the installed cluster then delegates to delivery.Revisions or Helm.History. No product offset cursor; bounds are defined by that reader/protocol, not handler List naming.',
'guarded-legacy-offset':'Existing exact list guard rejects supplied cursor and unsupported >10000 offset before legacy parsing; keep malformed/negative fallback. Stable tie ordering, matching count and browser use still require family review, not new safety proof.',
'signed-cursor':'Opt-in cursor= initial / signed continuation; legacy absent-cursor wire/count preserved. Actor/filter/order bindings and scope SQL qualification exist; representative selective prepared-plan cost remains open.',
'delivery-typed-offset':'Independent parsePagination rejects malformed/repeated/negative or >MaxInt32 offsets and limits outside20/default200max. Offsets above10000 are genuinely supported, so common legacy rejection must not be applied here blindly. Project-scoped query/count and stable tie ordering traced; critical consumers still need qualification.',
'unpaged-owned-collection':'Owned collection returned without a query offset. This identifies a preserved full-set contract, not pagination completion or a vulnerability. Bound/truthful completeness transition needs consumer and cardinality review before changing wire behavior.',
'bounded-page-no-offset':'Bounded query/window without request continuation. Must distinguish intended recent/top subset from full-collection claims; follow-up required where metadata/UI implies completeness.',
'provider-fixed-first-page':'Asynq ListArchivedTasks PageSize100 without Page; browser local table sees only first100. Actual provider101 fixture and compatible page owner required.',
'provider-registered-queue-catalog':'Queue set comes from provider registry. Per-queue inspector error currently emits zero fields without unknown marker; characterize and make status truthful.',
'charlie-bounded-offset-protocol':'Independent boundedQueryInt offset0..100000 and domain limit; not shared queryOffset clamp. Preserve distinct released contract and trace owner-bound service.',
'charlie-fixed-window':'Owner-bound recent50 threads at offset0, no request offset or continuation; qualify truncation semantics before advertising full history.',
'charlie-service-delegate':'Browser actor/admin gate precedes Charlie service/backend. Delegate determines collection bound and projection; do not infer global handler pagination from a List call.',
'scim-offset-protocol':'Independent SCIM startIndex/count/filter schema. Do not rewrite as product SQL cursor or globally clamp query strings.',
'guarded-parent-delegate':'Internal helper reached from parent list guard; guard evidence belongs to caller. No independent route/validator added.',
'kubernetes-collection-delegate':'Kubernetes list protocol with opaque continuation/resourceVersion must be traced through its reader; product SQL cursor is inapplicable.',
'aggregate-projection':'Collection-shaped detail/summary. Explicit caps/complete-estate versus subset semantics need review; not an offset-list endpoint.',
'fixed-catalog':'Code-defined catalog, no growable durable row list. Offset continuation is inapplicable.',
'cursor-parser-helper':'Shared parser helper, not HTTP endpoint. Signed mode validation is separately qualified.',
'mutation-internal-collection':'Collection read is used in a mutation/validation path, not GET paging. Preserve transaction/selection semantics; no response-page middleware.',
'detail-embedded-collection':'Detail endpoint embeds child records/recent events. Child cap and completeness are independent from collection route pagination.',
'bounded-estate-with-sentinel':'Estate collection checks +1 sentinel and returns explicit estate_limit_exceeded beyond10000; complete within declared bound, no silent partial total.',
'selected-input-projection':'Projection is selected by caller IDs; bound input and authorization rather than invent offset traversal.',
'full-scoped-collection':'Scoped full-set SQL or owned configuration collection has no page/window in handler; preserve released contract until explicit bounded opt-in.',
'full-user-scoped-collection':'Per-user preferences/views full-set collection with write cap/ownership independent of SQL pages; verify schema/domain cap.',
'bounded-ranking':'Top/ranking result intentionally limit-based; exact total must not be inferred from returned length.',
'bounded-time-series':'Recent/time-window history intentionally capped; total_count presently returned length needs truthful documented subset semantics.',
'bounded-activity-feed':'Activity feed merges bounded recent inputs; not an exact estate count. Consumer must treat snapshot as subset.',
'bounded-recommendation':'Recommendation ranking has bounded query limit and no offset; count denotes returned recommendations, not full chart estate.',
'aggregate-render':'Dashboard render composes selected widgets/data. SQL List call is not a separate public collection endpoint.',
'public-settings-projection':'Known public settings namespace projection; no arbitrary-row pagination or privileged values authority.',
'external-directory-search':'Search delegates to identity/provider results, separate bounded provider contract.',
'cross-cluster-search-delegate':'Search across scoped clusters delegates finite Kubernetes search; partial/error status must be preserved.',
'prometheus-protocol-delegate':'Prometheus series/metadata reader is an external protocol; inspect bounded body/time and truthful provider results separately.',
'internal-filter-expansion':'Internal authorization/filter repository expansion; memory/query cost distinct from HTTP page size.',
'signed-cursor-delegate':'Project signed path delegate; trace caller ListCharts plus signed codec/current authorization, not duplicate endpoint.',
}
rows=[]
for i,r in enumerate(old['handler_candidates']):
 p=root/r['path'];s,ff=funcs(p);matches=[f for f in ff if f['function']==r['function'] and f['receiver']==r['receiver']]
 assert len(matches)==1,(i,r['path'],r['function'])
 f=matches[0];body=f['body'];typ=re.search(r'\*?(\w+Handler)',r['receiver'] or '');typ=typ[1] if typ else None
 rr=[x for x in routes if x['handler_type']==typ and x['function']==r['function']]
 calls=sorted(set(re.findall(r'\b(?:\w+\.)*(\w+)\(',body)))
 guards=[g for g in ['validateLegacyPage','validatePageOffset','readCursorRequest','parsePagination','boundedQueryInt'] if g in calls]
 category=overrides.get(i)
 if not category:
  if 'readCursorRequest' in calls:category='signed-cursor'
  elif 'validateLegacyPage' in calls or 'validatePageOffset' in calls:category='guarded-legacy-offset'
  elif '/delivery/' in r['path'] and 'parsePagination' in calls:category='delivery-typed-offset'
  elif any(c.startswith('queryLimit') for c in calls) or re.search(r'Limit:\s*\d|limit\s*:=\s*\d',body):category='bounded-page-no-offset'
  else:category='unpaged-owned-collection'
 evidence=[]
 for n,line in enumerate(s.splitlines(),1):
  if f['line']<=n<=s.count('\n',0,f['end'])+1 and re.search(r'queryLimit|queryOffset|parsePagination|boundedQueryInt|validateLegacyPage|validatePageOffset|RequireSuperuser|requirePermission|authorize|browserCharlieActor|\.gate\(|paging\.|Count\w*\(|List\w*\(|RespondJSON|RespondRequestError|\.Context\(\)',line): evidence.append({'line':n,'text':line.strip()})
 rows.append(dict(id=f'h{i:03}',original_index=i,source=r['path'],function=r['function'],receiver=r['receiver'],line=f['line'],module_sha256=sha(s),function_sha256=sha(body),classification=category,classification_reason=notes[category],route_bindings=rr,guards=guards,direct_calls=calls,sql_bindings={c:sql[c] for c in calls if c in sql},source_evidence=evidence,qualification='SOURCE_CLASSIFICATION_ONLY; runtime coverage and full client/count semantics not implied'))
# Every OpenAPI candidate receives explicit traced owner or protocol/delegate
# category; path matching normalizes parameter names, not authorization scope.
def normalize(p):return re.sub(r'\{[^}]+\}','{}',p.rstrip('/')).replace('/api/v1/*/','/api/v1/')
additional_owners={
'GetResourceDiscovery':('internal/handler/resource_discovery.go','ResourceHandler','kubernetes-discovery-page','parseCRDDiscoveryPage/discoverCRDPage returns crd_continue, initial builtin matrix, explicit partial/errors. Own Kubernetes token protocol rather than SQL offset.'),
'Inventory':('internal/handler/service_mesh.go','ServiceMeshHandler','kubernetes-aggregate-projection','Per-kind listMeshInventoryItems consumes one envelope and ignores metadata.continue; returns count=len items and blank notice on success. Characterization required for omitted continuation/truthful total, not qualified complete inventory.'),
'Commands':('internal/handler/charlie_threads.go','CharlieThreadHandler','fixed-command-catalog','browserCharlieActor plus code-owned CharlieCommandCatalog; no growable SQL row set.'),
'History':('internal/handler/charlie_sessions.go','CharlieSessionHandler','charlie-history-cursor','Actor/session scoped service.History with bounded limit and cursor <=128, safe browser projection; preserve distinct opaque service contract.'),
'Search':('internal/handler/charlie_context.go','CharlieContextHandler','charlie-context-search','browser actor, query <=128 and bounded domain limit, owner-bound service search, no total or next page advertised.'),
'PodLogs':('internal/handler/workloads_resources.go','WorkloadHandler','log-protocol-not-row-list','Kubelet log protocol forwards tail/since/follow/previous and returns line projection; collection-shaped OpenAPI is not SQL row paging. Decode bound/error handling remains independently reviewed, not silently deemed complete.'),
}
additional=[]
for name,(path,typ,category,reason) in additional_owners.items():
 s,ff=funcs(root/path);f=next(f for f in ff if f['function']==name and typ in (f['receiver'] or ''))
 additional.append(dict(id='x'+name,source=path,line=f['line'],module_sha256=sha(s),function_sha256=sha(f['body']),handler_type=typ,function=name,classification=category,reason=reason))
operations=[]
for i,op in enumerate(ops['operations']):
 hits=[r for r in routes if normalize(r['path'])==normalize(op['path'])]
 linked=[r['id'] for r in rows if any(x in hits for x in r['route_bindings'])]
 extra=[x for x in additional if any(x['handler_type']==r['handler_type'] and x['function']==r['function'] for r in hits)]
 protocol_alias=None
 if not hits:
  assert op['path'] in ['/api/v1/clusters/{cluster_id}/k8s/*','/api/v1/clusters/{cluster_id}/k8s/{path}'],op['path']
  proxy_source='internal/server/routes_long_lived.go';proxy_text=(root/proxy_source).read_text()
  proxy_line=next(i+1 for i,l in enumerate(proxy_text.splitlines()) if 'HandleFunc(\"/api/v1/clusters/{cluster_id}/k8s/*\"' in l)
  protocol_alias=dict(source=proxy_source,module_sha256=sha(proxy_text),line=proxy_line,selector='deps.StreamingInternal.Proxy.HandleK8sProxy',registration_evidence='\n'.join(proxy_text.splitlines()[proxy_line-11:proxy_line]))
 operations.append(dict(protocol_alias_evidence=protocol_alias,additional_owner_ids=[x['id'] for x in extra], original_index=i,operation_id=op['operation_id'],path=op['path'],query_parameters=op['query_parameters'],runtime_candidates=linked,route_evidence=hits,classification='traced-candidate-family' if linked else ('noncandidate-route-owner' if hits else 'raw-kubernetes-proxy-alias'),reason='Bound to concrete dependency type/method and retained registration lines; aliases share handler family.' if hits else 'Both spec raw Kubernetes patterns are aliases of routes_long_lived.go HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", deps.StreamingInternal.Proxy.HandleK8sProxy). Canonical path/auth/scope/native RBAC and tunnel forwarding preserve upstream list query/Continue; no product SQL pager applies.'))
result=dict(schema_version=1,source_commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),scope='Finite source reconciliation of all discovery candidates; classification is not whole W07 runtime acceptance',original_inputs={k:sha(input_text(k)) for k in ['runtime-handler-candidates.json','openapi-list-candidates.json','critical-manual-classification.json']},family_definitions=notes,runtime_candidates=rows,additional_route_owners=additional,openapi_candidates=operations,counts=dict(runtime=len(rows),openapi=len(operations),families=dict(collections.Counter(r['classification'] for r in rows)),linked_openapi=sum(bool(r['runtime_candidates']) for r in operations),noncandidate_routes=sum(bool(r['route_evidence']) and not r['runtime_candidates'] for r in operations),raw_proxy_aliases=sum(not r['route_evidence'] for r in operations),every_original_id_has_family=True))
args.output.write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result['counts'],indent=2))
print('Unresolved registration')
for r in operations:
 if not r['route_evidence']:print(r['original_index'],r['path'])
print('Noncandidate route owners')
for r in operations:
 if r['route_evidence'] and not r['runtime_candidates']:print(r['original_index'],r['path'],[(x['handler_type'],x['function']) for x in r['route_evidence']])
