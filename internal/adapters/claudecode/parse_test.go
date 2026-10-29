package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
		if string(first) != string(second) || strings.Count(string(first), " hook claude-code Stop\"") != 1 || !strings.Contains(string(first), " hook claude-code StopFailure") {
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
		} else if err != nil || !sameJSON(restored, []byte(original)) {
			t.Fatalf("settings changed: %q, %v", restored, err)
		}
	}
}

func TestLifecycleBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []event.Kind
	}{
		{"StopFailure", `{"session_id":"s","hook_event_name":"StopFailure"}`, []event.Kind{event.ToolEdit, event.TurnEnd}},
		{"SessionEnd", `{"session_id":"s"}`, []event.Kind{event.ToolEdit, event.TurnEnd, event.SessionEnd}},
		{"StopFailure", `{"session_id":"s","agent_id":"child"}`, nil},
		{"Stop", `{"session_id":"s","agent_id":"child"}`, nil},
		{"PostToolUse", `{"session_id":"s","agent_id":"child","cwd":"/repo/sub","tool_name":"Write","tool_input":{"file_path":"a.go"}}`, []event.Kind{event.ToolEdit}},
		{"SessionStart", `{}`, nil},
	} {
		t.Run(tc.name+tc.body, func(t *testing.T) {
			got, err := ParseEvents(tc.name, []byte(tc.body), "/repo")
			if err != nil || len(got) != len(tc.want) {
				t.Fatalf("events: %+v, %v", got, err)
			}
			for i, kind := range tc.want {
				if got[i].Kind != kind {
					t.Fatalf("events: %+v", got)
				}
			}
			if tc.name == "PostToolUse" && (len(got[0].Paths) != 1 || got[0].Paths[0] != "/repo/sub/a.go") {
				t.Fatalf("relative path: %+v", got)
			}
		})
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func TestInstallPreservesHookFieldsAndSubsequentEdits(t *testing.T) {
	root := t.TempDir()
	p := settingsPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"hooks":{"PostToolUse":[{"matcher":"Read","customGroup":{"enabled":true},"hooks":[{"type":"command","command":"echo keep","timeout":12,"async":true},{"type":"http","url":"https://example.invalid/hook","headers":{"X-Custom":"value"}}]}]},"permissions":{"allow":["Read"]}}`)
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	hooks := cfg["hooks"].(map[string]any)
	group := hooks["PostToolUse"].([]any)[0].(map[string]any)
	entries := group["hooks"].([]any)
	if group["customGroup"].(map[string]any)["enabled"] != true || entries[0].(map[string]any)["timeout"] != float64(12) || entries[0].(map[string]any)["async"] != true || entries[1].(map[string]any)["url"] != "https://example.invalid/hook" {
		t.Fatalf("lost existing fields: %s", b)
	}
	traceGroup := hooks["PostToolUse"].([]any)[1].(map[string]any)
	if traceGroup["matcher"] != "Edit|Write|MultiEdit|NotebookEdit|Bash" {
		t.Fatalf("Trace tool matcher changed: %s", b)
	}
	cfg["later"] = "keep"
	b, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]any
	if err := json.Unmarshal(original, &expected); err != nil {
		t.Fatal(err)
	}
	expected["later"] = "keep"
	if err := json.Unmarshal(got, &cfg); err != nil || !reflect.DeepEqual(cfg, expected) {
		t.Fatalf("uninstall changed settings: %s %v", got, err)
	}
}

func TestReinstallDoesNotRestoreStaleSettings(t *testing.T) {
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	p := settingsPath(root)
	original := []byte(`{"user_setting":"new value"}`)
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil || !sameJSON(got, original) {
		t.Fatalf("stale restore: %s %v", got, err)
	}
}

func TestInstallRetainsLargeNumbers(t *testing.T) {
	root := t.TempDir()
	p := settingsPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"large":9007199254740993}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(got), "9007199254740993") {
		t.Fatalf("number changed: %s %v", got, err)
	}
}
