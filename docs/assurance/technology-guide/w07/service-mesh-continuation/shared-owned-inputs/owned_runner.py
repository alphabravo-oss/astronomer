import os,signal,subprocess,time
from pathlib import Path

class OwnershipLost(RuntimeError):pass

def _observe_exit(process):
 # WNOWAIT keeps the exited leader unreaped, reserving its PID/group identity.
 try:
  observed=os.waitid(os.P_PID,process.pid,os.WEXITED|os.WNOHANG|os.WNOWAIT)
 except ChildProcessError as error:raise OwnershipLost('owned leader was externally reaped') from error
 return observed is not None and observed.si_pid==process.pid

def _live_descendants(group):
 # This qualification runner is Linux-only. An exited descendant zombie owns no
 # live process/FD; keeping our leader unreaped prevents group identity reuse.
 found=[]
 for directory in Path('/proc').iterdir():
  if not directory.name.isdigit() or int(directory.name)==group:continue
  try:raw=(directory/'stat').read_text()
  except (FileNotFoundError,ProcessLookupError):continue
  fields=raw[raw.rfind(')')+2:].split()
  if len(fields)<3:raise RuntimeError('process inventory incomplete')
  if int(fields[2])==group and fields[0] not in ('Z','X'):found.append(int(directory.name))
 return found

def _signal_owned_group(process,action):
 # Verify unreaped child ownership immediately before every group signal.
 _observe_exit(process)
 try:os.killpg(process.pid,action)
 except ProcessLookupError:pass

def drain_owned_group(process,terminate=False,grace=2,kill_grace=3):
 actions=[]
 try:
  exited=_observe_exit(process);descendants=_live_descendants(process.pid)
  if terminate or not exited or descendants:
   _signal_owned_group(process,signal.SIGTERM);actions.append('TERM')
   until=time.monotonic()+grace
   while time.monotonic()<until:
    exited=_observe_exit(process);descendants=_live_descendants(process.pid)
    if exited and not descendants:break
    time.sleep(.02)
   if not exited or descendants:
    _signal_owned_group(process,signal.SIGKILL);actions.append('KILL')
  until=time.monotonic()+kill_grace
  while time.monotonic()<until:
   exited=_observe_exit(process);descendants=_live_descendants(process.pid)
   if exited and not descendants:
    # Reap ONLY after the held leader and live descendants are fully drained.
    process.wait(timeout=.5)
    return True,actions,[]
   time.sleep(.02)
  return False,actions,descendants
 except OwnershipLost:return False,actions,['ownership-lost-no-signal']
 except Exception:return False,actions,['drain-inventory-failed']

def run_owned(command,log,cwd,env,capacity,timeout,interval=10,grace=2,kill_grace=3):
 started=time.monotonic();result={'launched':False,'exitCode':None,'reason':'','guardException':None,'drained':True,'cleanupActions':[],'leaderHeldUntilDrain':False,'liveDescendantsAfterDrain':[]}
 process=None
 with log.open('wb') as stream:
  try:
   if timeout<=0:raise TimeoutError('overall deadline elapsed before launch')
   capacity('prelaunch')
   process=subprocess.Popen(command,cwd=cwd,env=env,stdout=stream,stderr=subprocess.STDOUT,start_new_session=True)
   result['launched']=True;result['leaderHeldUntilDrain']=True
   deadline=started+timeout;next_check=started
   while not _observe_exit(process):
    now=time.monotonic()
    if now>=deadline:raise TimeoutError('owned child deadline exceeded')
    if now>=next_check:
     capacity('maintained');next_check=time.monotonic()+interval
    time.sleep(min(.05,max(.001,deadline-time.monotonic())))
  except Exception as error:
   result['reason']='timeout' if isinstance(error,TimeoutError) else 'guard-or-launch-failure'
   result['guardException']={'type':type(error).__name__,'message':str(error)}
  finally:
   if process is not None:
    drained,actions,descendants=drain_owned_group(process,terminate=bool(result['reason']),grace=grace,kill_grace=kill_grace)
    result['drained']=drained;result['cleanupActions']=actions;result['liveDescendantsAfterDrain']=descendants;result['exitCode']=process.returncode
    if not drained:result['reason']='owned-group-not-drained'
   result['seconds']=round(time.monotonic()-started,3)
 return result

def restore_after_drain(originals,outcome):
 if not outcome.get('drained',False):return False
 for path,data in originals.items():path.write_bytes(data)
 return True
