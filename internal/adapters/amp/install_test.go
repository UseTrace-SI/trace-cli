package amp

import (
	"os"
	"os/exec"
	"path/filepath"
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
	if err != nil || !strings.Contains(string(b), root) || !strings.Contains(string(b), "agent.end") {
		t.Fatalf("plugin = %s, %v", b, err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("plugin remains: %v", err)
	}
}

func TestProjectUpgradePreservesEditedPlugin(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, renderProject(previousPlugin, "/old/trace", root), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if err := os.WriteFile(path, renderProject(previousPluginV2, "/old/trace", root), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatalf("upgrade recent version: %v", err)
	}
	if err := os.WriteFile(path, renderProject(previousPluginV3, "/old/trace", root), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatalf("upgrade previous version: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `send("tool_edit"`) {
		t.Fatalf("plugin not upgraded: %v", err)
	}
	modified := append(b, []byte("\n// user edit\n")...)
	if err := os.WriteFile(path, modified, 0600); err != nil {
		t.Fatal(err)
	}
	if Install(root) == nil || Uninstall(root) == nil {
		t.Fatal("modified plugin was not protected")
	}
	b, err = os.ReadFile(path)
	if err != nil || string(b) != string(modified) {
		t.Fatalf("plugin changed: %v", err)
	}
}

func TestProjectPluginRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "user.ts")
	if err := os.WriteFile(target, renderProject(plugin, "/bin/trace", root), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if Install(root) == nil || Uninstall(root) == nil {
		t.Fatal("accepted symlink")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
}

func TestProjectPluginEvents(t *testing.T) {
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
	binary := filepath.Join(dir, "fake-trace")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s ' \"$5\" >> \"$TRACE_AMP_LOG\"\ncat >> \"$TRACE_AMP_LOG\"\nprintf '\\n' >> \"$TRACE_AMP_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(dir, "trace.ts")
	if err := os.WriteFile(pluginPath, renderProject(plugin, binary, dir), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `import {pathToFileURL} from 'node:url'; import {readFileSync} from 'node:fs';
const plugin=(await import(pathToFileURL(process.argv[1]).href)).default;
const h={}; plugin({on:(name,fn)=>{h[name]=fn},helpers:{filesModifiedByToolCall:e=>e.input.files,filePathFromURI:f=>f}});
h['session.start']({thread:{id:'s'}}); h['agent.start']({thread:{id:'s'},message:'fix it'});
h['tool.result']({thread:{id:'s'},tool:'edit_file',status:'error',input:{files:['/repo/error']}});
h['tool.result']({thread:{id:'s'},tool:'edit_file',status:'done',input:{files:['/repo/a.go'],secret:'do not log'}});
h['agent.end']({thread:{id:'s'},messages:[{role:'assistant',content:[{type:'text',text:'done'}]}]});
let lines=[]; for(let n=0;n<100&&lines.length<6;n++){await new Promise(r=>setTimeout(r,20));try{const s=readFileSync(process.env.TRACE_AMP_LOG,'utf8');if(s.endsWith('\n'))lines=s.trim().split('\n')}catch{}}
if(lines.map(x=>x.split(' ')[0]).join(',')!=='session_start,turn_start,tool_edit,turn_context,tool_edit,turn_end')throw Error(JSON.stringify(lines));
const data=lines.map(x=>JSON.parse(x.slice(x.indexOf(' ')+1)));if(data.some(x=>x.session_id!=='s')||data[1].prompt!=='fix it'||data[2].paths[0]!=='/repo/a.go'||data[2].tool_name!=='edit_file'||data[3].text!=='done'||lines.join('').includes('do not log'))throw Error(JSON.stringify(data));`
	cmd := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e", script, pluginPath)
	cmd.Env = append(os.Environ(), "TRACE_AMP_LOG="+log)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("project plugin: %v: %s", err, out)
	}
}

func TestGlobalSetupPreservesUserFilesAndUpgrades(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	home := t.TempDir()
	p, err := globalPath(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetupGlobal(home, false); err != nil {
		t.Fatal(err)
	}
	old := []byte(strings.ReplaceAll(previousGlobalPlugin, "__TRACE_BIN__", `"/previous/trace"`))
	if err := os.WriteFile(p, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetupGlobal(home, false); err != nil {
		t.Fatalf("update executable path: %v", err)
	}
	old = []byte(strings.ReplaceAll(previousGlobalPluginV2, "__TRACE_BIN__", `"/previous/trace"`))
	if err := os.WriteFile(p, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetupGlobal(home, false); err != nil {
		t.Fatalf("update previous global plugin: %v", err)
	}
	if err := SetupGlobal(home, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("user plugin"), 0600); err != nil {
		t.Fatal(err)
	}
	if SetupGlobal(home, false) == nil || SetupGlobal(home, true) == nil {
		t.Fatal("did not protect user plugin")
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "user plugin" {
		t.Fatalf("user plugin changed: %q, %v", b, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if SetupGlobal(home, false) == nil {
		t.Fatal("accepted relative config directory")
	}
}

func TestGlobalPluginEvents(t *testing.T) {
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
	binary := filepath.Join(dir, "fake-trace")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s ' \"$3\" >> \"$TRACE_AMP_LOG\"\ncat >> \"$TRACE_AMP_LOG\"\nprintf '\\n' >> \"$TRACE_AMP_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(dir, "trace.ts")
	if err := os.WriteFile(pluginPath, renderGlobal(binary), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `import {pathToFileURL} from 'node:url';
import {readFileSync} from 'node:fs';
const plugin=(await import(pathToFileURL(process.argv[1]).href)).default;
const handlers={};
const amp={system:{executor:{kind:'local'},workspaceRoot:'file://workspace'},helpers:{filePathFromURI:f=>f==='file://workspace'?process.env.TRACE_TEST_ROOT:f,filesModifiedByToolCall:e=>e.input.files},on:(name,fn)=>{handlers[name]=fn}};
plugin(amp);
handlers['session.start']({thread:{id:'s'}});
handlers['agent.start']({thread:{id:'s'},message:'fix it'});
handlers['tool.result']({thread:{id:'s'},tool:'edit_file',status:'done',input:{files:['/repo/a.go'],secret:'do not log'}});
handlers['agent.end']({thread:{id:'s'},messages:[{role:'assistant',content:[{type:'text',text:'done'}]}]});
let lines=[];
for (let n=0;n<100 && lines.length<6;n++) {
  await new Promise(r=>setTimeout(r,20));
  try { const data=readFileSync(process.env.TRACE_AMP_LOG,'utf8'); if (data.endsWith('\n')) lines=data.trim().split('\n'); } catch {}
}
if (lines.map(x=>x.split(' ')[0]).join(',') !== 'session_start,turn_start,tool_edit,turn_context,tool_edit,turn_end') throw Error(JSON.stringify(lines));
const records=lines.map(x=>JSON.parse(x.slice(x.indexOf(' ')+1)));
if (records.some(x=>x.cwd!==process.env.TRACE_TEST_ROOT || x.session_id!=='s') || records[1].prompt!=='fix it' || records[2].paths[0]!=='/repo/a.go' || records[3].text!=='done' || lines.join('').includes('do not log')) throw Error(JSON.stringify(records));
const remoteHandlers={};
plugin({...amp,system:{executor:{kind:'orb'},workspaceRoot:'file://workspace'},on:(name,fn)=>{remoteHandlers[name]=fn}});
remoteHandlers['agent.start']({thread:{id:'remote'},message:'do not capture'});
await new Promise(r=>setTimeout(r,50));
if (readFileSync(process.env.TRACE_AMP_LOG,'utf8').includes('remote')) throw Error('captured remote workspace');`
	cmd := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e", script, pluginPath)
	cmd.Env = append(os.Environ(), "TRACE_AMP_LOG="+log, "TRACE_TEST_ROOT="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("global plugin: %v: %s", err, out)
	}
}
