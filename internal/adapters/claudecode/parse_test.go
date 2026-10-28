package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          event.Kind
		paths         int
	}{
		{"SessionStart", `{"session_id":"s","hook_event_name":"SessionStart"}`, event.SessionStart, 0},
		{"UserPromptSubmit", `{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"hello"}`, event.TurnStart, 0},
		{"PostToolUse", `{"session_id":"s","hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":"/repo/a.go"}}`, event.ToolEdit, 1},
		{"Stop", `{"session_id":"s","hook_event_name":"Stop"}`, event.TurnEnd, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events, err := Parse(tc.name, []byte(tc.payload), "/repo")
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].Kind != tc.want || len(events[0].Paths) != tc.paths {
				t.Fatalf("events: %+v", events)
			}
		})
	}
}

func TestStopKeepsFinalAssistantMessage(t *testing.T) {
	events, err := Parse("Stop", []byte(`{"session_id":"s","hook_event_name":"Stop","last_assistant_message":"Finished the edit."}`), "/repo")
	if err != nil || len(events) != 1 || events[0].Kind != event.TurnEnd || events[0].Text != "Finished the edit." {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestInstallUninstall(t *testing.T) {
	for _, original := range []string{"", "{\n  \"other\": true\n}\n", "{\"hooks\":{\"Stop\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"echo keep\"}]}]}}\n"} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
			t.Fatal(err)
		}
		p := settingsPath(root)
		if original != "" {
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := Install(root); err != nil {
			t.Fatal(err)
		}
		first, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := Install(root); err != nil {
			t.Fatal(err)
		}
		second, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(first) != string(second) || strings.Count(string(first), " hook claude-code Stop") != 1 {
			t.Fatal("install not idempotent")
		}
		if err := Uninstall(root); err != nil {
			t.Fatal(err)
		}
		restored, err := os.ReadFile(p)
		if original == "" {
			if !os.IsNotExist(err) {
				t.Fatalf("expected settings removal, got %v", err)
			}
		} else if err != nil || string(restored) != original {
			t.Fatalf("settings changed: %q, %v", restored, err)
		}
	}
}
