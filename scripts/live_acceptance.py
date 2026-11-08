#!/usr/bin/env python3
"""Run a real installed agent against disposable Trace fixtures.

Uses existing provider authentication without copying credentials or changing
user-level hooks. Never bypasses hook trust or agent permission checks. The
receipt records versions and capture assertions, never prompts or responses.
"""
import argparse
import hashlib
import json
import os
import pathlib
import shutil
import signal
import sqlite3
import subprocess
import tempfile
import time

parser=argparse.ArgumentParser()
parser.add_argument('--agent',choices=['claude-code','codex'],required=True)
parser.add_argument('--trace',required=True)
parser.add_argument('--receipt',required=True)
parser.add_argument('--manifest',help='stack.json from the same verified archive as the Trace binary')
args=parser.parse_args()
binary=pathlib.Path(args.trace).resolve()
agent='claude' if args.agent=='claude-code' else 'codex'
version=subprocess.check_output([agent,'--version'],text=True,stderr=subprocess.DEVNULL).strip()
started=time.time()
receipt={'agent':args.agent,'agent_version':version,'capture_mode':'rollout' if args.agent=='codex' else 'hooks','status':'unverified','checks':{},'started_at':int(started*1000)}
receipt['trace_binary_sha256']=hashlib.sha256(binary.read_bytes()).hexdigest()
if args.manifest:
 manifest=json.loads(pathlib.Path(args.manifest).read_text())
 receipt['stack_version']=manifest['version']
 receipt['sources']={name:source['commit'] for name,source in manifest['sources'].items()}

with tempfile.TemporaryDirectory(prefix='trace-live-') as directory:
 root=pathlib.Path(directory)/'repo';root.mkdir()
 tools=pathlib.Path(directory)/'bin';tools.mkdir();shutil.copy2(binary,tools/'trace')
 env=dict(os.environ,PATH=str(tools)+os.pathsep+os.environ['PATH'],TRACE_COMPANIONS='0',TRACE_HOME=str(pathlib.Path(directory)/'trace-user'))
 # Owner-only Vaultly privileges must never be inherited by an agent run.
 env.pop('VAULTLY_HUMAN_MODE',None)
 subprocess.run(['git','init','-q',str(root)],check=True)
 (root/'acceptance.txt').write_text('before\n')
 subprocess.run([str(binary),'--repo',str(root),'init','--agent',args.agent],env=env,check=True,stdout=subprocess.DEVNULL)
 prompt='This is an isolated Trace acceptance fixture. Change only acceptance.txt to exactly "trace acceptance passed" followed by a newline. Do not commit, access the network, inspect credentials, or change any other file. Then reply done.'
 # The default Codex adapter follows the normal on-disk rollout. Ephemeral
 # sessions deliberately omit that file and cannot verify this capture mode.
 command=([agent,'-p','--permission-mode','acceptEdits','--max-budget-usd','1','--settings',str(root/'.claude/settings.local.json'),'--output-format','json',prompt] if args.agent=='claude-code' else [agent,'exec','--json','--sandbox','workspace-write',prompt])
 process=None
 try:
  if args.agent=='codex':
   # Initial baseline scanning must finish before this fixture creates a new
   # rollout, otherwise it can be mistaken for pre-existing session history.
   ready=root/'.trace/codex-offsets.json'
   startup_deadline=time.monotonic()+30
   while not ready.is_file() and time.monotonic()<startup_deadline:time.sleep(.1)
   receipt['checks']['adapter_started']=ready.is_file()
   if not ready.is_file():raise subprocess.TimeoutExpired('Codex capture adapter startup',30)
  process=subprocess.Popen(command,cwd=root,env=env,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=True)
  process.wait(timeout=90)
  receipt['checks']['agent_exited_successfully']=process.returncode==0
  receipt['checks']['expected_file_bytes']=(root/'acceptance.txt').read_text()=='trace acceptance passed\n'
  deadline=time.time()+5
  while time.time()<deadline:
   with sqlite3.connect(root/'.trace/trace.db') as db:
    turns=db.execute('SELECT count(*) FROM turns WHERE ended_at IS NOT NULL').fetchone()[0]
    frames=db.execute('SELECT count(*) FROM keyframes WHERE seq>0').fetchone()[0]
   if turns and frames:break
   time.sleep(.1)
  receipt['checks']['completed_turn_recorded']=turns>0
  receipt['checks']['keyframe_recorded']=frames>0
  receipt['status']='passed' if all(receipt['checks'].values()) else 'failed'
 except subprocess.TimeoutExpired:
  receipt['status']='blocked';receipt['reason']='agent_timeout_or_authentication_unavailable'
 finally:
  if process and process.poll() is None:
   os.killpg(process.pid,signal.SIGTERM)
   try:process.wait(timeout=5)
   except subprocess.TimeoutExpired:os.killpg(process.pid,signal.SIGKILL);process.wait()
  pidfile=root/'.trace/daemon.pid'
  # Only this fixture's own child daemon may be stopped. Its PID was written
  # into the directory created above; no user's project daemon is targeted.
  if pidfile.exists():
   try:os.kill(int(pidfile.read_text()),signal.SIGTERM)
   except ProcessLookupError:pass
receipt['finished_at']=int(time.time()*1000)
path=pathlib.Path(args.receipt);path.parent.mkdir(parents=True,exist_ok=True)
fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
with os.fdopen(fd,'w') as file:json.dump(receipt,file,indent=2);file.write('\n')
print(json.dumps(receipt))
raise SystemExit(0 if receipt['status']=='passed' else 1)
