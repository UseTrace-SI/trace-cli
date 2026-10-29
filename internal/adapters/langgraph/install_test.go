package langgraph

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRootLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	script := `import importlib.util, sys, types
pkg=types.ModuleType('langchain_core'); callbacks=types.ModuleType('langchain_core.callbacks')
callbacks.BaseCallbackHandler=type('BaseCallbackHandler', (), {})
sys.modules['langchain_core']=pkg; sys.modules['langchain_core.callbacks']=callbacks
spec=importlib.util.spec_from_file_location('bridge', sys.argv[1]); mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
handler=mod.TraceCallbackHandler(); events=[]
handler._send=lambda kind, run_id, **fields: events.append((kind, run_id, fields))
handler.on_chain_start(None, {'input':'hello'}, run_id='root')
handler.on_chain_start(None, {}, run_id='child', parent_run_id='root')
handler.on_chain_end({}, run_id='child', parent_run_id='root')
handler.on_chain_end({}, run_id='root')
assert [x[0] for x in events] == ['session_start','turn_start','turn_end','session_end'], events
assert events[1][2]['prompt'] == 'hello'
`
	cmd := exec.Command("python3", "-c", script, filepath.Join(root, relative))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bridge lifecycle: %v: %s", err, out)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
}
