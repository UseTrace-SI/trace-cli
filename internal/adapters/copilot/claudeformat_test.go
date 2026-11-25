package copilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestIsClaudeHookPayload(t *testing.T) {
	for body, want := range map[string]bool{
		// Claude Code's own payloads, as recorded in the golden tests.
		`{"session_id":"cc","cwd":"/r","hook_event_name":"SessionStart","transcript_path":"/t.jsonl"}`: false,
		`{"session_id":"cc","cwd":"/r","tool_name":"Write","tool_input":{"file_path":"a.go"}}`:         false,
		`{"session_id":"cc","cwd":"/r","hook_event_name":"Stop","last_assistant_message":"done"}`:      false,
		`not json`: false,
		// Copilot CLI: camelCase, and snake_case with an ISO timestamp.
		`{"sessionId":"s","timestamp":1704614400000,"cwd":"/r"}`:                                            true,
		`{"hook_event_name":"SessionStart","session_id":"s","timestamp":"2026-01-01T00:00:00Z","cwd":"/r"}`: true,
	} {
		if got := IsClaudeHookPayload([]byte(body)); got != want {
			t.Errorf("IsClaudeHookPayload(%s) = %v, want %v", body, got, want)
		}
	}
}

func TestParseClaudeHook(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kinds      []event.Kind
		path, tool string
		at         int64
	}{
		{"SessionStart", `{"hook_event_name":"SessionStart","session_id":"s","timestamp":"2026-01-01T00:00:00Z","cwd":"/repo"}`, []event.Kind{event.SessionStart}, "", "", 1767225600000},
		{"UserPromptSubmit", `{"sessionId":"s","timestamp":1704614400000,"cwd":"/repo","prompt":"fix"}`, []event.Kind{event.TurnStart}, "", "", 1704614400000},
		{"PostToolUse", `{"session_id":"s","timestamp":"2026-01-01T00:00:00Z","cwd":"/repo","tool_name":"Write","tool_input":{"file_path":"a.go"}}`, []event.Kind{event.ToolEdit}, "/repo/a.go", "create", 0},
		{"PostToolUse", `{"sessionId":"s","timestamp":1,"cwd":"/repo","toolName":"edit","toolArgs":"{\"path\":\"b.go\"}"}`, []event.Kind{event.ToolEdit}, "/repo/b.go", "edit", 1},
		{"PostToolUse", `{"session_id":"s","timestamp":"2026-01-01T00:00:00Z","tool_name":"Read","tool_input":{"file_path":"/repo/c.go"}}`, []event.Kind{event.FileRead}, "/repo/c.go", "view", 0},
		{"PostToolUse", `{"sessionId":"s","timestamp":1,"toolName":"grep"}`, nil, "", "", 0},
		{"Stop", `{"sessionId":"s","timestamp":1}`, []event.Kind{event.ToolEdit, event.TurnEnd}, "", "", 1},
		{"SessionEnd", `{"session_id":"s","timestamp":"2026-01-01T00:00:00Z"}`, []event.Kind{event.ToolEdit, event.TurnEnd, event.SessionEnd}, "", "", 0},
		{"StopFailure", `{"sessionId":"s","timestamp":1}`, nil, "", "", 0},
	} {
		t.Run(tc.name+tc.tool, func(t *testing.T) {
			events, err := ParseClaudeHook(tc.name, []byte(tc.body), "/repo")
			if err != nil || len(events) != len(tc.kinds) {
				t.Fatalf("ParseClaudeHook() = %+v, %v", events, err)
			}
			for i, ev := range events {
				if ev.Kind != tc.kinds[i] || ev.Agent != "copilot" || ev.AgentSessionID != "s" {
					t.Fatalf("event %d = %+v", i, ev)
				}
			}
			if tc.path != "" && (len(events[0].Paths) != 1 || events[0].Paths[0] != tc.path || events[0].ToolName != tc.tool) {
				t.Fatalf("event = %+v", events[0])
			}
			if tc.at != 0 && events[0].At != tc.at {
				t.Fatalf("At = %d, want %d", events[0].At, tc.at)
			}
		})
	}
	if _, err := ParseClaudeHook("Stop", []byte("{"), "/repo"); err == nil {
		t.Fatal("malformed payload parsed")
	}
}

func TestHasGlobalHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("COPILOT_HOME", "")
	if HasGlobalHook(home, "agentStop") {
		t.Fatal("hook reported before install")
	}
	if err := InstallGlobal(home); err != nil {
		t.Fatal(err)
	}
	if !HasGlobalHook(home, "agentStop") || HasGlobalHook(home, "preToolUse") {
		t.Fatal("global hook detection is wrong")
	}
	if _, err := os.Stat(filepath.Join(home, ".copilot", "hooks", "usetrace.json")); err != nil {
		t.Fatal(err)
	}
}
