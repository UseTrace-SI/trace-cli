package aider

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWrapperLifecycle(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	script := `import importlib.util, sys
spec=importlib.util.spec_from_file_location('bridge',sys.argv[1]); m=importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
class NoThread:
    def __init__(self,*args,**kwargs): pass
    def start(self): pass
m.threading.Thread=NoThread
class Coder:
    def run(self, message, *args, **kwargs):
        if message=='fail': raise ValueError('original failure')
        return 'answer:' + message
class Capture:
    maxsize=256
    def __init__(self): self.items=[]
    def qsize(self): return len(self.items)
    def put_nowait(self, item): self.items.append(item)
bridge=m.TraceAider(Coder()); capture=Capture(); bridge._events=capture
assert bridge.run('hello')=='answer:hello'
assert bridge.run('/tokens')=='answer:/tokens'
try: bridge.run('fail')
except ValueError as e: assert str(e)=='original failure'
else: raise AssertionError('exception swallowed')
bridge.finish()
assert [[item[0] for item in batch] for batch in capture.items] == [['turn_start'],['tool_edit','turn_context','turn_end'],['turn_start'],['tool_edit','turn_end'],['session_end']], capture.items
assert capture.items[1][1][1]['text']=='answer:hello' and capture.items[1][1][1]['context_type']=='response'
assert capture.items[0][0][1]['prompt']=='hello'
assert capture.items[0][0][1]['session_id']==capture.items[3][0][1]['session_id']
`
	cmd := exec.Command(python, "-c", script, filepath.Join(root, relative))
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper lifecycle: %v: %s", err, out)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCallsAndFlush(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	script := `import importlib.util, sys, threading
spec=importlib.util.spec_from_file_location('bridge',sys.argv[1]); m=importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
events=[]; started=threading.Event(); release=threading.Event()
def send(args, **kwargs):
    events.append(args[-1])
m.subprocess.run=send
class Coder:
    def run(self, message):
        if message == 'first':
            started.set()
            assert release.wait(2)
        return message
bridge=m.TraceAider(Coder())
worker=threading.Thread(target=lambda: bridge.run('first')); worker.start()
assert started.wait(2)
assert bridge.run('second') == 'second'
bridge.finish() # Must not end the session while a run is active.
release.set(); worker.join(2); assert not worker.is_alive()
bridge.finish(); assert bridge.flush(timeout=2)
assert events == ['turn_start','tool_edit','turn_context','turn_end','session_end'], events
# A blocked worker must not make flush exceed its deadline.
block=threading.Event(); entered=threading.Event()
def blocked(args, **kwargs): entered.set(); block.wait(2)
m.subprocess.run=blocked
bridge.run('third'); assert entered.wait(2)
assert not bridge.flush(timeout=0.01)
block.set(); assert bridge.flush(timeout=2)
`
	cmd := exec.Command(python, "-c", script, filepath.Join(root, relative))
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("concurrent lifecycle and flush: %v: %s", err, out)
	}
}

func TestAiderCallingConventions(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	script := `import importlib.util, sys
spec=importlib.util.spec_from_file_location('bridge',sys.argv[1]); m=importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
class NoThread:
    def __init__(self,*args,**kwargs): pass
    def start(self): pass
m.threading.Thread=NoThread
class Coder:
    def run(self, with_message=None, preproc=True): return (with_message, preproc)
traced=m.TraceAider(Coder())
assert traced.run()==(None,True)
assert traced.run(with_message='hello',preproc=False)==('hello',False)
assert traced.run('other',False)==('other',False)
assert traced.run(message='legacy')==('legacy',True)
queued=list(traced._events.queue)
assert [batch[0][0] for batch in queued]==['turn_start','tool_edit','turn_start','tool_edit','turn_start','tool_edit']
assert queued[0][0][1]['prompt']=='hello' and queued[2][0][1]['prompt']=='other'
try:
    from aider.coders import Coder as RealCoder
except ImportError:
    sys.exit(0)
class IO:
    def user_input(self, message): pass
real=RealCoder.__new__(RealCoder)
real.io=IO(); real.partial_response_content='real coder response'
calls=[]
real.run_one=lambda message,preproc: calls.append((message,preproc))
wrapped=m.TraceAider(real)
assert wrapped.run(with_message='real keyword',preproc=False)=='real coder response'
assert wrapped.run('real positional')=='real coder response'
assert calls==[('real keyword',False),('real positional',True)]
assert list(wrapped._events.queue)[0][0][1]['prompt']=='real keyword'
`
	cmd := exec.Command(python, "-c", script, filepath.Join(root, relative))
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Aider calling conventions: %v: %s", err, out)
	}
}

func TestUpgradeLegacyBridgePreservesEdits(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(legacyGenerated(root)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != generated(root) {
		t.Fatalf("legacy bridge not upgraded: %v", err)
	}
	modified := legacyGenerated(root) + "\n# user edit\n"
	if err := os.WriteFile(path, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("modified bridge was replaced")
	}
	if err := Uninstall(root); err == nil {
		t.Fatal("modified bridge was removed")
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != modified {
		t.Fatalf("modified bridge changed: %v", err)
	}
}

func TestUpgradePreviousBridge(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(previousGenerated(root)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != generated(root) {
		t.Fatalf("previous bridge not upgraded: %v", err)
	}
}

func TestChatModeCommands(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	script := `import importlib.util, sys
spec=importlib.util.spec_from_file_location('bridge',sys.argv[1]); m=importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
class NoThread:
    def __init__(self,*args,**kwargs): pass
    def start(self): pass
m.threading.Thread=NoThread
class Coder:
    def run(self, message, preproc=True): return message
bridge=m.TraceAider(Coder())
for message in ['/ask why?', '/code fix it', '/architect plan', '/context look', '/ask', '/tokens', '! echo ok']:
    assert bridge.run(message)==message
assert bridge.run('/literal', preproc=False)=='/literal'
starts=[batch[0][1]['prompt'] for batch in list(bridge._events.queue) if batch[0][0]=='turn_start']
assert starts==['/ask why?', '/code fix it', '/architect plan', '/context look', '/literal'], starts
`
	cmd := exec.Command(python, "-c", script, filepath.Join(root, relative))
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("chat mode commands: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, relative), []byte(responseBridgeGenerated(root)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatalf("upgrading response bridge: %v", err)
	}
}

func TestPreservesModifiedBridge(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, relative)
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("user content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("overwrote modified bridge")
	}
	if err := Uninstall(root); err == nil {
		t.Fatal("removed modified bridge")
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "user content" {
		t.Fatalf("changed: %s, %v", b, err)
	}
}
