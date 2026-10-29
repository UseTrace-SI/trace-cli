package crewai

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestListenerCorrelation(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	script := `import importlib.util, sys, types
pkg = types.ModuleType('crewai'); events = types.ModuleType('crewai.events')
class Start: pass
class End: pass
class Fail: pass
class Bus:
    def __init__(self): self.handlers={}
    def on(self, kind):
        def register(fn): self.handlers[kind]=fn; return fn
        return register
bus=Bus()
class Base:
    def __init__(self): self.setup_listeners(bus)
events.BaseEventListener=Base; events.CrewKickoffStartedEvent=Start; events.CrewKickoffCompletedEvent=End; events.CrewKickoffFailedEvent=Fail
sys.modules['crewai']=pkg; sys.modules['crewai.events']=events
spec=importlib.util.spec_from_file_location('bridge',sys.argv[1]); m=importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
class NoThread:
    def __init__(self, *args, **kwargs): pass
    def start(self): pass
m.threading.Thread=NoThread
listener=m.TraceCrewListener()
class Capture:
    def __init__(self): self.items=[]
    def put_nowait(self, item): self.items.append(item)
capture=Capture(); listener._events=capture
def obj(**kwargs): return types.SimpleNamespace(**kwargs)
bus.handlers[Start](None,obj(event_id='one', inputs={'prompt':'hello'}))
bus.handlers[Start](None,obj(event_id='two', inputs={'input':'second'}))
bus.handlers[End](None,obj(started_event_id='two'))
bus.handlers[End](None,obj(started_event_id=None))
bus.handlers[Fail](None,obj(started_event_id='one'))
bus.handlers[End](None,obj(started_event_id='one'))
assert [[ev[0] for ev in batch] for batch in capture.items] == [['session_start','turn_start'],['session_start','turn_start'],['tool_edit','turn_end','session_end'],['tool_edit','turn_end','session_end']], capture.items
assert capture.items[0][1][1]['prompt']=='hello'
assert capture.items[2][0][1]['session_id']=='two' and capture.items[3][0][1]['session_id']=='one'
`
	cmd := exec.Command(python, "-c", script, filepath.Join(root, relative))
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("listener: %v: %s", err, out)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRejectsModifiedBridge(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, relative)
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("user file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("overwrote user file")
	}
	if err := Uninstall(root); err == nil {
		t.Fatal("removed user file")
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "user file" {
		t.Fatalf("user file changed: %s, %v", b, err)
	}
}
