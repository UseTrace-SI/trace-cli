package hermes

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestSetupPreservesPlugin(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("HERMES_HOME", profile)
	p := filepath.Join(profile, "plugins", "trace-observer", "__init__.py")
	if err := Setup(t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.HasPrefix(string(b), marker) {
		t.Fatalf("plugin: %s, %v", b, err)
	}
	if err := Setup(t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	modified := string(b) + "# user addition\n"
	if err := os.WriteFile(p, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Setup(t.TempDir(), true); err == nil {
		t.Fatal("removed modified plugin")
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Setup(t.TempDir(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("plugin remains: %v", err)
	}
}

func TestParse(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		kind       event.Kind
		count      int
	}{
		{"pre_llm_call", `{"session_id":"s","user_message":"fix ghp_abcdefghijklmnopqrstuvwxyz"}`, event.TurnStart, 1},
		{"post_llm_call", `{"session_id":"s","assistant_response":"finished"}`, event.ToolEdit, 3},
		{"on_session_end", `{"session_id":"s"}`, event.ToolEdit, 2},
		{"post_tool_call", `{"session_id":"s","tool_name":"write_file","path":"a.go","status":"ok"}`, event.ToolEdit, 1},
		{"post_tool_call", `{"session_id":"s","tool_name":"read_file","status":"ok"}`, event.FileRead, 1},
		{"post_tool_call", `{"session_id":"s","tool_name":"write_file","status":"blocked"}`, "", 0},
		{"on_session_finalize", `{"session_id":"s"}`, event.ToolEdit, 3},
		{"pre_llm_call", `{}`, "", 0},
	} {
		t.Run(tt.name+tt.body, func(t *testing.T) {
			got, err := Parse(tt.name, []byte(tt.body), "/repo")
			if err != nil || len(got) != tt.count {
				t.Fatalf("events: %#v, %v", got, err)
			}
			if len(got) > 0 && (got[0].Kind != tt.kind || strings.Contains(got[0].Prompt, "ghp_abcdefghijklmnopqrstuvwxyz")) {
				t.Fatalf("event: %#v", got[0])
			}
		})
	}
}

func TestObserverDispatchesInOrder(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	t.Setenv("HERMES_HOME", dir)
	if err := Setup(t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(dir, "plugins", "trace-observer", "__init__.py")
	fake := filepath.Join(dir, "fake-trace")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s ' \"$3\" >> \"$TRACE_TEST_EVENTS\"\ncat >> \"$TRACE_TEST_EVENTS\"\nprintf '\\n' >> \"$TRACE_TEST_EVENTS\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "events")
	program := `import importlib.util, os, sys
spec = importlib.util.spec_from_file_location("observer", sys.argv[1]); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
m._TRACE = sys.argv[2]
class Context:
    def __init__(self): self.hooks = {}
    def register_hook(self, name, f): self.hooks[name] = f
ctx = Context(); m.register(ctx)
ctx.hooks["pre_llm_call"](session_id="child", parent_session_id="parent", user_message="ignore")
ctx.hooks["pre_llm_call"](session_id="one", user_message="hello")
ctx.hooks["post_tool_call"](session_id="one", tool_name="write_file", args={"path":"main.go"}, status="ok")
ctx.hooks["post_llm_call"](session_id="one", assistant_response="done")
ctx.hooks["on_session_finalize"](session_id="one")
m._events.join()
`
	cmd := exec.Command(python, "-c", program, plugin, fake)
	cmd.Env = append(os.Environ(), "TRACE_HERMES_REPO="+dir, "TRACE_TEST_EVENTS="+output, "PYTHONDONTWRITEBYTECODE=1")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("observer: %v: %s", err, b)
	}
	b, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "pre_llm_call ") || !strings.HasPrefix(lines[1], "post_tool_call ") || !strings.HasPrefix(lines[2], "post_llm_call ") || !strings.HasPrefix(lines[3], "on_session_finalize ") {
		t.Fatalf("unexpected event order: %s", b)
	}
}

func TestObserverClosesUnsuccessfulTurns(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	t.Setenv("HERMES_HOME", dir)
	if err := Setup(t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(dir, "plugins", "trace-observer", "__init__.py")
	program := `import importlib.util, os, sys
spec=importlib.util.spec_from_file_location('observer',sys.argv[1]); m=importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
class NoThread:
    def __init__(self,*args,**kwargs): pass
    def start(self): pass
m.threading.Thread=NoThread
class Context:
    def __init__(self): self.hooks={}
    def register_hook(self,name,fn): self.hooks[name]=fn
ctx=Context(); m.register(ctx)
for session, outcome in [('failed',{'failed':True}), ('incomplete',{'completed':False}), ('interrupted',{'interrupted':True})]:
    ctx.hooks['pre_llm_call'](session_id=session,user_message='hello')
    ctx.hooks['on_session_end'](session_id=session,**outcome)
ctx.hooks['pre_llm_call'](session_id='success',user_message='hello')
ctx.hooks['post_llm_call'](session_id='success',assistant_response='done')
ctx.hooks['on_session_end'](session_id='success',completed=True)
assert [x[0] for x in list(m._events.queue)] == ['pre_llm_call','on_session_end']*3+['pre_llm_call','post_llm_call']
`
	cmd := exec.Command(python, "-c", program, plugin)
	cmd.Env = append(os.Environ(), "TRACE_HERMES_REPO="+dir, "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("observer outcomes: %v: %s", err, out)
	}
}

func TestPreviousObserverUpgrade(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERMES_HOME", dir)
	if err := Setup(t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "plugins", "trace-observer", "__init__.py")
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previous := strings.Replace(string(current), `            if session not in _active or not (kwargs.get("interrupted") or kwargs.get("failed") is True or kwargs.get("completed") is False):`, `            if session not in _active or not kwargs.get("interrupted"):`, 1)
	if previous == string(current) {
		t.Fatal("previous observer was not reconstructed")
	}
	if err := os.WriteFile(path, []byte(previous), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Setup(t.TempDir(), false); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(current) {
		t.Fatalf("observer not upgraded: %v", err)
	}
}
