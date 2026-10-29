package kilo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestInstallUninstall(t *testing.T) {
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, relative)
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), root) || !strings.Contains(string(b), "chat.message") {
		t.Fatalf("plugin = %s, %v", b, err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("plugin remains: %v", err)
	}
}

func TestProjectPluginOwnership(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, relative)
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	old := projectPlugin("/old/trace", "/old/repository")
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatalf("refreshing managed plugin: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) == string(old) || !managedProject(got) {
		t.Fatalf("managed plugin was not refreshed: %v", err)
	}
	modified := append(got, []byte("\n// user edit\n")...)
	if err := os.WriteFile(path, modified, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("modified plugin was replaced")
	}
	if err := Uninstall(root); err == nil {
		t.Fatal("modified plugin was removed")
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != string(modified) {
		t.Fatalf("modified plugin changed: %v", err)
	}
}

func TestProjectPluginRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "user.ts")
	if err := os.WriteFile(target, projectPlugin("/bin/trace", root), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("install accepted symlink")
	}
	if err := Uninstall(root); err == nil {
		t.Fatal("uninstall accepted symlink")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
}

func TestPluginLifecycle(t *testing.T) {
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
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s ' \"$5\" >> \"$TRACE_KILO_LOG\"\ncat >> \"$TRACE_KILO_LOG\"\nprintf '\\n' >> \"$TRACE_KILO_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	data := strings.ReplaceAll(plugin, "__TRACE_BIN__", strconv.Quote(bin))
	data = strings.ReplaceAll(data, "__TRACE_ROOT__", strconv.Quote(dir))
	path := filepath.Join(dir, "trace.ts")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `import {pathToFileURL} from 'node:url'; import {readFileSync} from 'node:fs';
const p=(await import(pathToFileURL(process.argv[1]).href)).default; const hooks=await p.server();
hooks.event({event:{type:'session.created',properties:{info:{id:'s'}}}});
hooks['chat.message']({sessionID:'s'},{parts:[{type:'text',text:'fix it'}]});
hooks['tool.execute.after']({tool:'read',sessionID:'s',args:{filePath:'source.txt'}});
hooks['tool.execute.after']({tool:'edit',sessionID:'s',args:{filePath:'main.txt',newString:'secret raw input'}});
hooks['tool.execute.after']({tool:'bash',sessionID:'s',args:{command:'secret shell command'}});
hooks.event({event:{type:'message.updated',properties:{info:{id:'m',sessionID:'s',role:'assistant'}}}});
hooks.event({event:{type:'message.part.updated',properties:{part:{id:'p',sessionID:'s',messageID:'m',type:'text',text:'partial'}}}});
hooks.event({event:{type:'message.part.updated',properties:{part:{id:'p',sessionID:'s',messageID:'m',type:'text',text:'final answer'}}}});
hooks.event({event:{type:'message.part.updated',properties:{part:{id:'other',sessionID:'s',messageID:'user',type:'text',text:'ignore user'}}}});
hooks.event({event:{type:'session.idle',properties:{sessionID:'s'}}});
hooks.event({event:{type:'session.deleted',properties:{sessionID:'s'}}});
let lines=[];for(let n=0;n<100&&lines.length<9;n++){await new Promise(r=>setTimeout(r,20));try{const s=readFileSync(process.env.TRACE_KILO_LOG,'utf8');if(s.endsWith('\n'))lines=s.trim().split('\n')}catch{}}
if(lines.map(x=>x.split(' ')[0]).join(',')!=='session_start,turn_start,file_read,tool_edit,tool_edit,turn_context,tool_edit,turn_end,session_end')throw Error(JSON.stringify(lines));
const data=lines.map(x=>JSON.parse(x.slice(x.indexOf(' ')+1)));if(data.some(x=>x.session_id!=='s')||data[1].prompt!=='fix it'||data[2].paths[0]!==process.env.TRACE_ROOT+'/source.txt'||data[3].paths[0]!==process.env.TRACE_ROOT+'/main.txt'||data[4].paths.length!==0||data[5].text!=='final answer'||data[6].paths||lines.join('').includes('secret'))throw Error(JSON.stringify(data));`
	cmd := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e", script, path)
	cmd.Env = append(os.Environ(), "TRACE_KILO_LOG="+log, "TRACE_ROOT="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin lifecycle: %v: %s", err, out)
	}
}

func TestGlobalSetup(t *testing.T) {
	home := t.TempDir()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	path := filepath.Join(config, "kilo", "plugin", "trace-global.ts")
	if err := SetupGlobal(home, false); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{previousGlobalPlugin, previousGlobalPluginV2, previousGlobalPluginV3} {
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
	if err := os.WriteFile(path, []byte("user plugin"), 0600); err != nil {
		t.Fatal(err)
	}
	if SetupGlobal(home, false) == nil || SetupGlobal(home, true) == nil {
		t.Fatal("user plugin not protected")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "user plugin" {
		t.Fatal("user plugin changed")
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if SetupGlobal(home, false) == nil {
		t.Fatal("accepted relative config")
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
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s %s ' \"$2\" \"$3\" >> \"$TRACE_KILO_LOG\"\ncat >> \"$TRACE_KILO_LOG\"\nprintf '\\n' >> \"$TRACE_KILO_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "trace.ts")
	if err := os.WriteFile(path, renderGlobal(bin), 0600); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(dir, "project.ts")
	projectData := strings.ReplaceAll(plugin, "__TRACE_BIN__", strconv.Quote(bin))
	projectData = strings.ReplaceAll(projectData, "__TRACE_ROOT__", strconv.Quote(dir))
	if err := os.WriteFile(project, []byte(projectData), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `import {pathToFileURL} from 'node:url'; import {readFileSync} from 'node:fs';
const p=(await import(pathToFileURL(process.argv[1]).href)).default; const ctx={directory:process.env.TRACE_ROOT,worktree:process.env.TRACE_ROOT}; const hooks=await p.server(ctx);
hooks.event({event:{type:'session.created',properties:{info:{id:'s'}}}});
hooks['chat.message']({sessionID:'s'},{parts:[{type:'text',text:'fix it'}]});
hooks['tool.execute.after']({tool:'read',sessionID:'s',args:{filePath:'source.txt'}});
hooks['tool.execute.after']({tool:'edit',sessionID:'s',args:{filePath:'main.txt',newString:'secret raw input'}});
hooks['tool.execute.after']({tool:'bash',sessionID:'s',args:{command:'secret shell command'}});
hooks.event({event:{type:'message.updated',properties:{info:{id:'m',sessionID:'s',role:'assistant'}}}});
hooks.event({event:{type:'message.part.updated',properties:{part:{id:'p',sessionID:'s',messageID:'m',type:'text',text:'partial'}}}});
hooks.event({event:{type:'message.part.updated',properties:{part:{id:'p',sessionID:'s',messageID:'m',type:'text',text:'final answer'}}}});
hooks.event({event:{type:'message.part.updated',properties:{part:{id:'other',sessionID:'s',messageID:'user',type:'text',text:'ignore user'}}}});
hooks.event({event:{type:'session.idle',properties:{sessionID:'s'}}});
hooks.event({event:{type:'session.deleted',properties:{sessionID:'s'}}});
let lines=[];for(let n=0;n<100&&lines.length<9;n++){await new Promise(r=>setTimeout(r,20));try{const s=readFileSync(process.env.TRACE_KILO_LOG,'utf8');if(s.endsWith('\n'))lines=s.trim().split('\n')}catch{}}
if(lines.map(x=>x.split(' ')[1]).join(',')!=='session_start,turn_start,file_read,tool_edit,tool_edit,turn_context,tool_edit,turn_end,session_end')throw Error(JSON.stringify(lines));
const data=lines.map(x=>JSON.parse(x.slice(x.indexOf(' ',x.indexOf(' ')+1)+1)));
if(lines.some(x=>!x.startsWith('kilo-global '))||data.some(x=>x.session_id!=='s'||x.cwd!==process.env.TRACE_ROOT)||data[1].prompt!=='fix it'||data[2].paths[0]!==process.env.TRACE_ROOT+'/source.txt'||data[3].paths[0]!==process.env.TRACE_ROOT+'/main.txt'||data[4].paths.length!==0||data[5].text!=='final answer'||lines.join('').includes('secret'))throw Error(JSON.stringify(data));
if(Object.keys(await p.server({directory:'relative'})).length)throw Error('accepted relative workspace');
const project=(await import(pathToFileURL(process.argv[2]).href)).default;
const other={directory:process.env.TRACE_ROOT,worktree:'/'};
await project.server(other);if(other[Symbol.for('usetrace.kilo.project')])throw Error('wrong worktree claimed');
await project.server(ctx);hooks['chat.message']({sessionID:'s'},{parts:[{type:'text',text:'project overlap'}]});
for(let n=0;n<100;n++){await new Promise(r=>setTimeout(r,20));const s=readFileSync(process.env.TRACE_KILO_LOG,'utf8');if(s.includes('project overlap')){const last=s.trim().split('\n').at(-1);const obj=JSON.parse(last.slice(last.indexOf(' ',last.indexOf(' ')+1)+1));if(!obj.project_loaded)throw Error('loaded project not signalled');break}if(n===99)throw Error('missing overlap event')}`
	cmd := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e", script, path, project)
	cmd.Env = append(os.Environ(), "TRACE_KILO_LOG="+log, "TRACE_ROOT="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("global lifecycle: %v: %s", err, out)
	}
}
