package openclaude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestHooks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, ".openclaude", "settings.local.json"))
	if err != nil || !strings.Contains(string(b), " hook openclaude Stop") {
		t.Fatalf("hooks: %s, %v", b, err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".openclaude", "settings.local.json")); !os.IsNotExist(err) {
		t.Fatalf("hooks remain: %v", err)
	}
	for _, tc := range []struct {
		name, body string
		kinds      []event.Kind
	}{
		{"SessionStart", `{"session_id":"s"}`, []event.Kind{event.SessionStart}},
		{"UserPromptSubmit", `{"session_id":"s","prompt":"hello"}`, []event.Kind{event.TurnStart}},
		{"PostToolUse", `{"session_id":"s","tool_name":"Edit","tool_input":{"file_path":"a.go"}}`, []event.Kind{event.ToolEdit}},
		{"Stop", `{"session_id":"s"}`, []event.Kind{event.ToolEdit, event.TurnEnd}},
		{"StopFailure", `{"session_id":"s","hook_event_name":"StopFailure"}`, []event.Kind{event.ToolEdit, event.TurnEnd}},
		{"StopFailure", `{"session_id":"s","agent_id":"child","hook_event_name":"StopFailure"}`, nil},
		{"PostToolUse", `{"session_id":"s","agent_id":"child","tool_name":"Write","tool_input":{"file_path":"a.go"}}`, []event.Kind{event.ToolEdit}},
		{"UserPromptSubmit", `{"session_id":"s","agent_id":"child","prompt":"internal"}`, nil},
		{"SessionEnd", `{"session_id":"s"}`, []event.Kind{event.ToolEdit, event.TurnEnd, event.SessionEnd}},
		{"SessionStart", `{}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.name, []byte(tc.body), root)
			if err != nil || len(got) != len(tc.kinds) {
				t.Fatalf("events: %+v, %v", got, err)
			}
			for i, kind := range tc.kinds {
				if got[i].Kind != kind || got[i].Agent != "openclaude" {
					t.Fatalf("events: %+v", got)
				}
			}
		})
	}
	got, err := Parse("Stop", []byte(`{"session_id":"s","last_assistant_message":"done"}`), root)
	if err != nil || len(got) != 3 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnContext || got[2].Kind != event.TurnEnd {
		t.Fatalf("stop: %+v, %v", got, err)
	}
}

func TestRelativeToolPathUsesSessionDirectory(t *testing.T) {
	got, err := Parse("PostToolUse", []byte(`{"session_id":"s","cwd":"/repo/sub","tool_name":"Edit","tool_input":{"file_path":"a.go"}}`), "/repo")
	if err != nil || len(got) != 1 || len(got[0].Paths) != 1 || got[0].Paths[0] != "/repo/sub/a.go" {
		t.Fatalf("events: %+v, %v", got, err)
	}
}
