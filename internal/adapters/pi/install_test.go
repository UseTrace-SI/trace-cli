package pi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
)

func TestInstallUninstall(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, relative)
	for i := 0; i < 2; i++ {
		if err := Install(root); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), marker) || !strings.Contains(string(b), root) || strings.Contains(string(b), "__TRACE_BIN__") {
		t.Fatalf("installed Pi extension = %s, %v", b, err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Pi extension remains: %v", err)
	}
}

func TestInstallPreservesUserExtension(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("export default () => {};\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("expected user extension conflict")
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "export default () => {};\n" {
		t.Fatalf("user extension changed: %s, %v", b, err)
	}
}

func TestGlobalSetup(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("PI_CONFIG_DIR", "")
	t.Setenv("OMP_PROFILE", "")
	t.Setenv("PI_PROFILE", "")
	for _, agent := range []string{"pi", "omp"} {
		t.Run(agent, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "."+agent, "agent", "extensions", "trace-global", "index.ts")
			if err := SetupGlobal(home, agent, false); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, renderGlobalVersion(previousGlobalExtension, agent, "/old/trace"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := SetupGlobal(home, agent, false); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil || strings.Contains(string(b), "/old/trace") || !strings.Contains(string(b), `const agentName = "`+agent+`"`) {
				t.Fatalf("installed: %s %v", b, err)
			}
			if err := SetupGlobal(home, agent, true); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("user extension"), 0600); err != nil {
				t.Fatal(err)
			}
			if SetupGlobal(home, agent, false) == nil || SetupGlobal(home, agent, true) == nil {
				t.Fatal("user extension not protected")
			}
			b, _ = os.ReadFile(path)
			if string(b) != "user extension" {
				t.Fatal("user extension changed")
			}
		})
	}
	t.Setenv("PI_CODING_AGENT_DIR", "relative")
	if SetupGlobal(t.TempDir(), "pi", false) == nil {
		t.Fatal("accepted relative agent directory")
	}
	shared := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", shared)
	if err := SetupGlobal(t.TempDir(), "pi", false); err != nil {
		t.Fatal(err)
	}
	if SetupGlobal(t.TempDir(), "omp", false) == nil || SetupGlobal(t.TempDir(), "omp", true) == nil {
		t.Fatal("did not protect a different agent's extension in the shared directory")
	}
	t.Setenv("OMP_PROFILE", "work")
	home := t.TempDir()
	if err := SetupGlobal(home, "omp", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".omp", "profiles", "work", "agent", "extensions", "trace-global", "index.ts")); err != nil {
		t.Fatal(err)
	}
}

func TestOmpGlobalAgentDir(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		name, profile, legacy, config, override, want string
		invalid                                       bool
	}{
		{"default", "", "work", "", "", ".omp/agent", false},
		{"default sentinel", " default ", "", "", "", ".omp/agent", false},
		{"custom root", "", "", ".custom-omp", "", ".custom-omp/agent", false},
		{"named profile ignores override", " work-1 ", "", ".custom-omp", "/tmp/custom", ".custom-omp/profiles/work-1/agent", false},
		{"bad traversal", "../escape", "", "", "", "", true},
		{"reserved name", "con.log", "", "", "", "", true},
		{"trailing dot", "work.", "", "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OMP_PROFILE", tc.profile)
			t.Setenv("PI_PROFILE", tc.legacy)
			t.Setenv("PI_CONFIG_DIR", tc.config)
			t.Setenv("PI_CODING_AGENT_DIR", tc.override)
			got, err := globalAgentDir(home, "omp")
			if tc.invalid {
				if err == nil {
					t.Fatal("expected invalid profile error")
				}
				return
			}
			if err != nil || got != filepath.Join(home, tc.want) {
				t.Fatalf("directory = %q, %v", got, err)
			}
		})
	}
	t.Run("legacy profile fallback", func(t *testing.T) {
		t.Setenv("OMP_PROFILE", "")
		if err := os.Unsetenv("OMP_PROFILE"); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PI_PROFILE", "legacy")
		t.Setenv("PI_CONFIG_DIR", "")
		t.Setenv("PI_CODING_AGENT_DIR", "")
		got, err := globalAgentDir(home, "omp")
		if err != nil || got != filepath.Join(home, ".omp", "profiles", "legacy", "agent") {
			t.Fatalf("directory = %q, %v", got, err)
		}
	})
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
	for _, agent := range []string{"pi", "omp"} {
		t.Run(agent, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "events")
			binary := filepath.Join(dir, "fake-trace")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s %s ' \"$2\" \"$3\" >> \"$TRACE_PI_LOG\"\ncat >> \"$TRACE_PI_LOG\"\nprintf '\\n' >> \"$TRACE_PI_LOG\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			plugin := filepath.Join(dir, "trace.ts")
			if err := os.WriteFile(plugin, renderGlobal(agent, binary), 0600); err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(dir, "project.ts")
			if err := os.WriteFile(project, Render(dir, agent), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0600); err != nil {
				t.Fatal(err)
			}
			script := `import {pathToFileURL} from 'node:url';
import {readFileSync} from 'node:fs';
const plugin=(await import(pathToFileURL(process.argv[1]).href)).default;
const handlers={}; plugin({on:(name,fn)=>{handlers[name]=fn}});
const ctx={cwd:process.env.TRACE_TEST_ROOT,isProjectTrusted:()=>true,sessionManager:{getSessionId:()=> 's',getSessionFile:()=> 'session'}};
handlers.session_start({},ctx); handlers.before_agent_start({prompt:'fix it'},ctx);
handlers.tool_execution_start({toolCallId:'t',toolName:'edit',args:{path:'main.txt'}});
handlers.tool_execution_end({toolCallId:'t'},ctx);
handlers.agent_end({messages:[{role:'assistant',content:[{type:'text',text:'done'}]}]},ctx);
handlers.session_shutdown({},ctx);
let lines=[];
for(let n=0;n<100 && lines.length<7;n++){await new Promise(r=>setTimeout(r,20));try{const s=readFileSync(process.env.TRACE_PI_LOG,'utf8');if(s.endsWith('\n'))lines=s.trim().split('\n')}catch{}}
const kinds=lines.map(x=>x.split(' ')[1]).join(',');
if(kinds!=='session_start,turn_start,tool_edit,turn_context,tool_edit,turn_end,session_end')throw Error(JSON.stringify(lines));
const records=lines.map(x=>JSON.parse(x.slice(x.indexOf(' ',x.indexOf(' ')+1)+1)));
if(lines.some(x=>!x.startsWith(process.env.TRACE_AGENT+'-global ')) || records.some(x=>x.cwd!==process.env.TRACE_TEST_ROOT||x.session_id!=='s') || records[1].prompt!=='fix it'||records[2].paths[0]!=='main.txt')throw Error(JSON.stringify(records));
const project=(await import(pathToFileURL(process.argv[2]).href)).default;
project({on:()=>{}});
handlers.before_agent_start({prompt:'duplicate'},ctx);
handlers.before_agent_start({prompt:'ignored'}, {...ctx,sessionManager:{...ctx.sessionManager,getSessionFile:()=>undefined}});
await new Promise(r=>setTimeout(r,50));const data=readFileSync(process.env.TRACE_PI_LOG,'utf8');if(data.includes('ignored')||data.includes('duplicate'))throw Error('captured disabled or duplicate session');`
			cmd := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e", script, plugin, project)
			cmd.Env = append(os.Environ(), "TRACE_PI_LOG="+log, "TRACE_TEST_ROOT="+dir, "TRACE_AGENT="+agent)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("plugin: %v: %s", err, out)
			}
		})
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
	for _, agent := range []string{"pi", "omp"} {
		t.Run(agent, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "events")
			binary := filepath.Join(dir, "fake-trace")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s ' \"$5\" >> \"$TRACE_PI_LOG\"\ncat >> \"$TRACE_PI_LOG\"\nprintf '\\n' >> \"$TRACE_PI_LOG\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			plugin := filepath.Join(dir, "project.ts")
			content := strings.Replace(string(Render(dir, agent)), strconv.Quote(hookcmd.Path()), strconv.Quote(binary), 1)
			if err := os.WriteFile(plugin, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0600); err != nil {
				t.Fatal(err)
			}
			script := `import {pathToFileURL} from 'node:url';
import {readFileSync} from 'node:fs';
const plugin=(await import(pathToFileURL(process.argv[1]).href)).default;
const handlers={};plugin({on:(name,fn)=>{handlers[name]=fn}});
const ctx={sessionManager:{getSessionId:()=> 's',getSessionFile:()=> 'session'}};
handlers.session_start({},ctx);handlers.before_agent_start({prompt:'fix it'},ctx);
handlers.tool_execution_start({toolCallId:'t',toolName:'edit',args:{path:'main.txt'}});
handlers.tool_execution_end({toolCallId:'t'},ctx);
handlers.agent_end({messages:[{role:'assistant',content:[{type:'text',text:'done'}]}]},ctx);
handlers.session_shutdown({},ctx);
let lines=[];for(let n=0;n<100&&lines.length<7;n++){await new Promise(r=>setTimeout(r,20));try{const s=readFileSync(process.env.TRACE_PI_LOG,'utf8');if(s.endsWith('\n'))lines=s.trim().split('\n')}catch{}}
if(lines.map(x=>x.split(' ')[0]).join(',')!=='session_start,turn_start,tool_edit,turn_context,tool_edit,turn_end,session_end')throw Error(JSON.stringify(lines));
const records=lines.map(x=>JSON.parse(x.slice(x.indexOf(' ')+1)));
if(records.some(x=>x.session_id!=='s')||records[1].prompt!=='fix it'||records[2].paths[0]!=='main.txt'||records[4].paths)throw Error(JSON.stringify(records));
handlers.before_agent_start({prompt:'ignored'},{sessionManager:{getSessionId:()=>{throw Error('closed')}}});
await new Promise(r=>setTimeout(r,30));if(readFileSync(process.env.TRACE_PI_LOG,'utf8').includes('ignored'))throw Error('captured closed session');`
			cmd := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e", script, plugin)
			cmd.Env = append(os.Environ(), "TRACE_PI_LOG="+log)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("project plugin: %v: %s", err, out)
			}
		})
	}
}

func TestProjectPluginDoesNotKeepProcessAlive(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	help, err := exec.Command(node, "--help").Output()
	if err != nil || !strings.Contains(string(help), "--experimental-strip-types") {
		t.Skip("node does not support TypeScript stripping")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "slow-trace")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 3\n"), 0700); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(dir, "project.ts")
	content := strings.Replace(string(Render(dir, "pi")), strconv.Quote(hookcmd.Path()), strconv.Quote(binary), 1)
	if err := os.WriteFile(plugin, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `import {pathToFileURL} from 'node:url';
const plugin=(await import(pathToFileURL(process.argv[1]).href)).default;
const handlers={};plugin({on:(name,fn)=>{handlers[name]=fn}});
handlers.session_start({}, {sessionManager:{getSessionId:()=> 's',getSessionFile:()=> 'session'}});`
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--experimental-strip-types", "--input-type=module", "-e", script, plugin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin kept process alive: %v: %s", err, out)
	}
}
