package opencode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSetupGlobal(t *testing.T) {
	home := t.TempDir()
	config := t.TempDir()
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", config)
	path := filepath.Join(config, "opencode", "plugins", "usetrace", "index.ts")
	legacy := filepath.Join(config, "opencode", "plugins", "usetrace-v1.ts")
	if err := SetupGlobal(home, false); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(legacy); err != nil || !managedLegacy(b) {
		t.Fatalf("missing v1 plugin: %v", err)
	}
	for _, version := range []string{previousGlobalPlugin, previousGlobalPluginV2} {
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(version, "__TRACE_BIN__", strconv.Quote("/old/trace"))), 0600); err != nil {
			t.Fatal(err)
		}
		if err := SetupGlobal(home, false); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil || strings.Contains(string(b), "/old/trace") {
			t.Fatalf("upgrade: %s %v", b, err)
		}
	}
	if err := SetupGlobal(home, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("v1 plugin not removed: %v", err)
	}
	if err := os.WriteFile(legacy, []byte("user plugin"), 0600); err != nil {
		t.Fatal(err)
	}
	if SetupGlobal(home, false) == nil || SetupGlobal(home, true) == nil {
		t.Fatal("user v1 plugin not protected")
	}
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user plugin"), 0600); err != nil {
		t.Fatal(err)
	}
	if SetupGlobal(home, false) == nil || SetupGlobal(home, true) == nil {
		t.Fatal("user plugin not protected")
	}
	t.Setenv("OPENCODE_CONFIG_DIR", "relative")
	if SetupGlobal(home, false) == nil {
		t.Fatal("accepted relative config path")
	}
}

func TestLegacyPluginLifecycle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	help, err := exec.Command(node, "--help").Output()
	if err != nil || !strings.Contains(string(help), "--experimental-strip-types") {
		t.Skip("node does not support TypeScript stripping")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "events")
	bin := filepath.Join(dir, "trace")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s %s ' \"$2\" \"$3\" >> \"$TRACE_OC_LOG\"\ncat >> \"$TRACE_OC_LOG\"\nprintf '\\n' >> \"$TRACE_OC_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "index.ts")
	if err := os.WriteFile(path, renderLegacy(bin), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `import {pathToFileURL} from 'node:url'; import {readFileSync} from 'node:fs';
const p=(await import(pathToFileURL(process.argv[1]).href)).default;
const hooks=await p({directory:process.env.TRACE_ROOT});
await hooks['chat.message']({sessionID:'s'},{parts:[{type:'text',text:'fix it'},{type:'text',text:'secret synthetic',synthetic:true}]});
await hooks['tool.execute.after']({sessionID:'s',tool:'edit',args:{filePath:'main.txt',newString:'secret input'}});
await hooks['tool.execute.after']({sessionID:'other',tool:'read',args:{filePath:'outside.txt'}});
await hooks.event({event:{type:'session.status',properties:{sessionID:'s',status:{type:'idle'}}}});
await hooks.event({event:{type:'session.idle',properties:{sessionID:'s'}}});
let lines=[];for(let n=0;n<150&&lines.length<5;n++){await new Promise(r=>setTimeout(r,20));try{const s=readFileSync(process.env.TRACE_OC_LOG,'utf8');if(s.endsWith('\n'))lines=s.trim().split('\n')}catch{}}
const kinds=lines.map(x=>x.split(' ')[1]);if(kinds.join(',')!=='session_start,turn_start,tool_edit,tool_edit,turn_end')throw Error(JSON.stringify(lines));
const records=lines.map(x=>JSON.parse(x.slice(x.indexOf(' ',x.indexOf(' ')+1)+1)));
if(lines.some(x=>!x.startsWith('opencode-v1-global '))||records.some(x=>x.cwd!==process.env.TRACE_ROOT||x.session_id!=='s')||records[1].prompt!=='fix it'||records[2].paths[0]!==process.env.TRACE_ROOT+'/main.txt'||lines.join('').includes('secret')||lines.join('').includes('outside.txt'))throw Error(JSON.stringify(records));`
	cmd := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e", script, path)
	cmd.Env = append(os.Environ(), "TRACE_OC_LOG="+log, "TRACE_ROOT="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("v1 plugin: %v: %s", err, out)
	}
}

func TestGlobalPluginLifecycle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	help, err := exec.Command(node, "--help").Output()
	if err != nil || !strings.Contains(string(help), "--experimental-strip-types") {
		t.Skip("node does not support TypeScript stripping")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "events")
	bin := filepath.Join(dir, "trace")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s %s ' \"$2\" \"$3\" >> \"$TRACE_OC_LOG\"\ncat >> \"$TRACE_OC_LOG\"\nprintf '\\n' >> \"$TRACE_OC_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "index.ts")
	if err := os.WriteFile(path, renderGlobal(bin), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `import {pathToFileURL} from 'node:url'; import {readFileSync} from 'node:fs';
const p=(await import(pathToFileURL(process.argv[1]).href)).default;
let prompt, resolveNext, signal; const events=[];
function push(event){if(resolveNext){const r=resolveNext;resolveNext=undefined;r(event)}else events.push(event)}
const ctx={location:{directory:process.env.TRACE_ROOT},session:{hook:async(n,fn)=>{if(n!=='prompt')throw Error(n);prompt=fn}},event:{subscribe:({signal:s})=>{signal=s;return {async *[Symbol.asyncIterator](){while(!s.aborted)yield events.length?events.shift():await new Promise(r=>resolveNext=r)}}}}};
const cleanup=await p.setup(ctx);prompt({sessionID:'s',prompt:{text:'uncommitted'}});
const event=(type,data,location)=>push({type,data,location});
event('session.inbox.enqueued',{sessionID:'s',inboxID:'a',item:{type:'user',payload:{text:'first'},delivery:'steer'}});
event('session.inbox.delivered',{sessionID:'s',inboxID:'a'});
event('session.tool.input.started',{sessionID:'s',assistantMessageID:'m',id:'r',name:'read'});
event('session.tool.called',{sessionID:'s',assistantMessageID:'m',id:'r',input:{filePath:'source.txt'}});
event('session.tool.success',{sessionID:'s',assistantMessageID:'m',id:'r',content:['secret output']});
event('session.tool.input.started',{sessionID:'s',assistantMessageID:'m',id:'w',name:'edit'});
event('session.tool.called',{sessionID:'s',assistantMessageID:'m',id:'w',input:{filePath:'main.txt',newString:'secret input'}});
event('session.tool.success',{sessionID:'s',assistantMessageID:'m',id:'w'});
event('session.tool.input.started',{sessionID:'s',assistantMessageID:'m',id:'bad',name:'read'});
event('session.tool.called',{sessionID:'s',assistantMessageID:'m',id:'bad',input:{filePath:'failed.txt'}});
event('session.tool.failed',{sessionID:'s',assistantMessageID:'m',id:'bad'});
event('session.reasoning.ended',{sessionID:'s',text:'think'});
event('session.text.ended',{sessionID:'s',text:'done'});
event('session.inbox.enqueued',{sessionID:'s',inboxID:'b',item:{type:'user',payload:{text:'queued'},delivery:'queue'}});
event('session.inbox.enqueued',{sessionID:'s',inboxID:'c',item:{type:'user',payload:{text:'steer'},delivery:'steer'}});
event('session.inbox.enqueued',{sessionID:'s',inboxID:'x',item:{type:'user',payload:{text:'cancelled'},delivery:'queue'}});
event('session.inbox.cancelled',{sessionID:'s',inboxID:'x'});
event('session.inbox.delivered',{sessionID:'s',inboxID:'c'});
event('session.execution.succeeded',{sessionID:'s'});
event('session.inbox.delivered',{sessionID:'s',inboxID:'b'});
event('session.execution.failed',{sessionID:'s'});
let lines=[];for(let n=0;n<150&&lines.length<16;n++){await new Promise(r=>setTimeout(r,20));try{const s=readFileSync(process.env.TRACE_OC_LOG,'utf8');if(s.endsWith('\n'))lines=s.trim().split('\n')}catch{}}
const kinds=lines.map(x=>x.split(' ')[1]);if(kinds.join(',')!=='session_start,turn_start,file_read,tool_edit,turn_context,turn_context,tool_edit,turn_end,session_start,turn_start,tool_edit,turn_end,session_start,turn_start,tool_edit,turn_end')throw Error(JSON.stringify(lines));
const records=lines.map(x=>JSON.parse(x.slice(x.indexOf(' ',x.indexOf(' ')+1)+1)));
if(lines.some(x=>!x.startsWith('opencode-global '))||records.some(x=>x.cwd!==process.env.TRACE_ROOT||x.session_id!=='s')||records.filter((_,i)=>kinds[i]==='turn_start').map(x=>x.prompt).join(',')!=='first,steer,queued'||records[2].paths[0]!==process.env.TRACE_ROOT+'/source.txt'||records[3].paths[0]!==process.env.TRACE_ROOT+'/main.txt'||records[4].context_type!=='thinking'||records[5].text!=='done'||lines.join('').includes('secret')||lines.join('').includes('failed.txt'))throw Error(JSON.stringify(records));
cleanup();if(!signal.aborted)throw Error('stream was not cancelled');`
	cmd := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e", script, path)
	cmd.Env = append(os.Environ(), "TRACE_OC_LOG="+log, "TRACE_ROOT="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("global plugin: %v: %s", err, out)
	}
}
