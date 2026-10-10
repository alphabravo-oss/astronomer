import json,pathlib,re,collections,hashlib
out=pathlib.Path('/tmp/w07-cost-3c405b96');data=[]
for major in [17,18]:
 p=out/('postgres'+str(major)+'-test.jsonl')
 if not p.exists():continue
 events=[];plans=[];counters=[];pending=None
 def nodes(plan):
  yield plan
  for sub in plan.get('Plans',[]):yield from nodes(sub)
 for line in p.read_text().splitlines():
  try:r=json.loads(line)
  except ValueError:continue
  events.append(r);text=r.get('Output','')
  if 'representative100k ' in text:
   start=text.find('[',text.find('representative100k '))
   assert start>=0
   pending={'test':r['Test'],'header':text[:start],'raw':text[start:]}
  elif pending is not None and r.get('Test')==pending['test'] and r['Action']=='output':pending['raw']+=text
  if pending is not None:
   try:raw=json.loads(pending['raw'])
   except json.JSONDecodeError:continue
   assert len(raw)==1
   text=pending['header'];start=len(text);test=pending['test'];pending=None
   scans=[]
   for node in nodes(raw[0]['Plan']):
    if 'Scan' in node['Node Type']:
     loops=node.get('Actual Loops',0);removed=node.get('Rows Removed by Filter',0);recheck=node.get('Rows Removed by Index Recheck',0);rows=node.get('Actual Rows',0)
     scans.append({'node_type':node['Node Type'],'index_name':node.get('Index Name'),'actual_rows':rows,'actual_loops':loops,'rows_removed_by_filter':removed,'rows_removed_by_index_recheck':recheck,'examined_returned_plus_removed_times_loops':(rows+removed+recheck)*loops,'shared_hit_blocks':node.get('Shared Hit Blocks'),'shared_read_blocks':node.get('Shared Read Blocks')})
   plans.append({'test':test,'query':'After' if 'After' in text[:start] else 'First','advertised_continuation':False if 'no advertised continuation' in text else None,'execution_ms':raw[0].get('Execution Time'),'planning_ms':raw[0].get('Planning Time'),'scan_nodes':scans})
  if 'eight production executions mode=' in text:
   m=re.search(r'mode=(\w+) counters before=\(generic:(\d+) custom:(\d+)\) after=\(generic:(\d+) custom:(\d+)\) delta=\(generic:(\d+) custom:(\d+)\)',text);assert m
   counters.append({'test':r['Test'],'mode':m[1],'before_generic':int(m[2]),'before_custom':int(m[3]),'after_generic':int(m[4]),'after_custom':int(m[5]),'delta_generic':int(m[6]),'delta_custom':int(m[7])})
 terminal=[r for r in events if r.get('Test') and r['Action'] in ['pass','fail','skip']]
 data.append({'major':major,'raw_sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'named_test_terminal_counts':dict(collections.Counter(r['Action'] for r in terminal)),'plan_count':len(plans),'counter_count':len(counters),'measurements':plans,'counters':counters})
(out/'structured-results.json').write_text(json.dumps({'schema_version':1,'source':'3c405b964fcd81911154a9a21579c5d95fc66f3e','fixture_rows':100000,'scope':'transaction-local synthetic characterization, no fleet-capacity claim','results':data},indent=2)+'\n')
for d in data:
 print(json.dumps({'major':d['major'],'terminal':d['named_test_terminal_counts'],'plans':d['plan_count'],'counters':d['counter_count'],'scans_over201':[{'test':p['test'].split('/',1)[1],'query':p['query'],'scan':n['node_type'],'index':n['index_name'],'examined':n['examined_returned_plus_removed_times_loops']} for p in d['measurements'] for n in p['scan_nodes'] if n['examined_returned_plus_removed_times_loops']>201]},indent=2))
