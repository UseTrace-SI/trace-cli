package zoo

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupArchiveAndPreservation(t *testing.T) {
	home := t.TempDir()
	if err := Setup(home, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, file := range z.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name] = string(body)
	}
	if !json.Valid([]byte(files["extension/package.json"])) || !strings.Contains(files["extension/extension.js"], "ZooCodeOrganization.zoo-code") || files["extension.vsixmanifest"] == "" {
		t.Fatalf("incomplete VSIX: %v", files)
	}
	if err := Setup(home, false); err != nil {
		t.Fatal(err)
	}
	old, err := archive("/old/trace")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(home), old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Setup(home, false); err != nil {
		t.Fatalf("updating old executable path: %v", err)
	}
	legacy, err := archiveWithExtension("/old/trace", legacyExtension)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(home), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Setup(home, false); err != nil {
		t.Fatalf("updating previous observer: %v", err)
	}
	if err := Setup(home, true); err != nil {
		t.Fatal(err)
	}
	if err := Setup(home, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(home), []byte("user data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Setup(home, false); err == nil {
		t.Fatal("overwrote modified archive")
	}
	if err := Setup(home, true); err == nil {
		t.Fatal("removed modified archive")
	}
}

func TestObserverFiltersWorkspacesAndForwardsTaskLifecycle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "events")
	binary := filepath.Join(dir, "fake-trace")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s ' \"$3\" >> \"$TRACE_ZOO_LOG\"\ncat >> \"$TRACE_ZOO_LOG\"\nprintf '\\n' >> \"$TRACE_ZOO_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	quoted, err := json.Marshal(binary)
	if err != nil {
		t.Fatal(err)
	}
	bridge := filepath.Join(dir, "extension.js")
	if err := os.WriteFile(bridge, []byte(strings.ReplaceAll(extension, "__TRACE_BIN__", string(quoted))), 0600); err != nil {
		t.Fatal(err)
	}
	script := `const Module = require('node:module');
const { EventEmitter } = require('node:events');
const fs = require('node:fs');
const root = process.env.TRACE_TEST_ROOT;
const api = new EventEmitter();
api.getTaskHistoryItem = async (id) => ({
  main: {workspace:root,task:'fix main'},
  child: {workspace:root,task:'subtask',parentTaskId:'main'},
  foreign: {workspace:'/other/workspace',task:'other'},
}[id]);
const vscode = { extensions: {getExtension: () => ({activate: async () => api})},
  workspace: {workspaceFolders:[{uri:{scheme:'file',fsPath:root}}]} };
const load = Module._load;
Module._load = function(id, ...args) { return id === 'vscode' ? vscode : load.call(this,id,...args); };
const bridge = require(process.argv[1]);
bridge.activate();
setImmediate(() => {
  api.emit('taskStarted','main'); api.emit('taskStarted','child');
  api.emit('taskStarted','foreign'); api.emit('taskStarted','missing');
  api.emit('message',{taskId:'main',action:'created',message:{type:'say',say:'user_feedback',text:'tool approval'}});
  api.emit('message',{taskId:'main',action:'updated',message:{type:'say',say:'completion_result',text:'first answer',partial:false}});
  api.emit('message',{taskId:'main',action:'created',message:{type:'ask',ask:'completion_result',partial:true}});
  api.emit('message',{taskId:'main',action:'updated',message:{type:'ask',ask:'completion_result',partial:false}});
  api.emit('message',{taskId:'main',action:'created',message:{type:'say',say:'user_feedback',text:'please revise'}});
  api.emit('taskCompleted','main'); api.emit('taskCompleted','main');
});
(async () => {
  let lines=[];
  for (let n=0; n<100 && lines.length<7; n++) {
    await new Promise(r => setTimeout(r,20));
    try {
      const data=fs.readFileSync(process.env.TRACE_ZOO_LOG,'utf8');
      if (data.endsWith('\n')) lines=data.trim().split('\n');
    } catch {}
  }
  const kinds=lines.map(line => line.split(' ')[0]);
  if (kinds.join(',') !== 'turn_start,tool_edit,turn_end,turn_start,tool_edit,turn_end,session_end') throw Error(JSON.stringify(lines));
  const payloads=lines.map(line => JSON.parse(line.slice(line.indexOf(' ')+1)));
  if (payloads.some(p => p.session_id !== 'main' || p.cwd !== root) || payloads[0].prompt !== 'fix main' || payloads[2].text !== 'first answer' || payloads[3].prompt !== 'please revise') throw Error(JSON.stringify(payloads));
  bridge.deactivate();
  if (api.listenerCount('taskStarted') !== 0) throw Error('listeners not removed');
})().catch(e => { console.error(e); process.exitCode=1; });`
	cmd := exec.Command(node, "-e", script, bridge)
	cmd.Env = append(os.Environ(), "TRACE_TEST_ROOT="+dir, "TRACE_ZOO_LOG="+log)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("observer lifecycle: %v: %s", err, out)
	}
}
