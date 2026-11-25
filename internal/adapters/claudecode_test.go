package adapters

import (
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/copilot"
)

// Copilot CLI runs .claude/settings*.json hooks too. Its calls are dropped
// when Trace's own Copilot hook records the same event.
func TestClaudeCodeSkipsCopilotDuplicates(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COPILOT_HOME", "")
	t.Setenv("TRACE_HOME", t.TempDir())
	call := func(event, body string) *HookCall {
		return NewHookCall("claude-code", "claude-code", event, false, strings.NewReader(body))
	}
	const cp = `{"sessionId":"s","timestamp":1,"cwd":"/r"}`
	const cc = `{"session_id":"s","cwd":"/r","transcript_path":"/t"}`
	a := claudeCode{}
	if a.SkipHook(call("Stop", cp), root) || a.SkipHook(call("Stop", cc), root) {
		t.Fatal("skipped without a Copilot hook")
	}
	if !a.SkipHook(call("StopFailure", cp), root) {
		t.Fatal("kept a Copilot event Trace does not record")
	}
	if err := copilot.Install(root); err != nil {
		t.Fatal(err)
	}
	if !a.SkipHook(call("Stop", cp), root) || a.SkipHook(call("Stop", cc), root) {
		t.Fatal("project Copilot hook dedupe is wrong")
	}
	if err := copilot.Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if err := copilot.InstallGlobal(home); err != nil {
		t.Fatal(err)
	}
	if !a.SkipHook(call("UserPromptSubmit", cp), root) {
		t.Fatal("global Copilot hook dedupe is wrong")
	}
}
