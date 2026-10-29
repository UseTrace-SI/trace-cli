package qwen

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
		name, body string
		kind       event.Kind
		count      int
	}{
		{"SessionStart", `{"session_id":"s","hook_event_name":"SessionStart"}`, event.SessionStart, 1},
		{"UserPromptSubmit", `{"session_id":"s","submitted_prompt":"fix it"}`, event.TurnStart, 1},
		{"PostToolUse", `{"session_id":"s","tool_name":"write_file","tool_input":{"file_path":"/repo/a.go"}}`, event.ToolEdit, 1},
		{"Stop", `{"session_id":"s","last_assistant_message":"done"}`, event.ToolEdit, 3},
		{"SessionEnd", `{"session_id":"s"}`, event.ToolEdit, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.name, []byte(tc.body), "/repo")
			if err != nil || len(got) != tc.count || got[0].Kind != tc.kind || got[0].AgentSessionID != "s" {
				t.Fatalf("Parse() = %+v, %v", got, err)
			}
			if tc.name == "Stop" && (got[1].Kind != event.TurnContext || got[2].Kind != event.TurnEnd) {
				t.Fatalf("Stop did not capture response and close turn: %+v", got)
			}
			if tc.name == "SessionEnd" && (got[1].Kind != event.TurnEnd || got[2].Kind != event.SessionEnd) {
				t.Fatalf("SessionEnd did not close turn: %+v", got)
			}
		})
	}
	if got, err := Parse("UserPromptSubmit", []byte(`{"session_id":"s","prompt":"internal tool continuation"}`), "/repo"); err != nil || len(got) != 0 {
		t.Fatalf("continuation opened a turn: %+v, %v", got, err)
	}
	if got, err := Parse("Stop", []byte(`{"session_id":"s","agent_id":"child"}`), "/repo"); err != nil || len(got) != 0 {
		t.Fatalf("subagent closed parent turn: %+v, %v", got, err)
	}
	if got, err := Parse("StopFailure", []byte(`{"session_id":"s"}`), "/repo"); err != nil || len(got) != 2 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnEnd {
		t.Fatalf("failed turn not closed with a scan: %+v, %v", got, err)
	}
	if got, err := Parse("PostToolUse", []byte(`{"session_id":"s","cwd":"/repo/sub","tool_name":"write_file","tool_input":{"file_path":"a.go"}}`), "/repo"); err != nil || len(got) != 1 || len(got[0].Paths) != 1 || got[0].Paths[0] != "/repo/sub/a.go" {
		t.Fatalf("relative tool path: %+v, %v", got, err)
	}
	if got, err := Parse("SessionStart", []byte(`{}`), "/repo"); err != nil || len(got) != 0 {
		t.Fatalf("missing session identity: %+v, %v", got, err)
	}
}

func TestGlobalInstallAndProjectDetection(t *testing.T) {
	t.Setenv("QWEN_HOME", "")
	home := t.TempDir()
	p := filepath.Join(home, ".qwen", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"user-hook"}]}]},"other":true}`)
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := InstallGlobal(home); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "user-hook") || strings.Count(string(b), " hook qwen-global StopFailure") != 1 {
		t.Fatalf("settings = %s, %v", b, err)
	}
	if err := UninstallGlobal(home); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(p)
	if err != nil || !sameJSON(b, original) {
		t.Fatalf("settings changed: %s, %v", b, err)
	}
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if !HasProjectHook(root, "StopFailure") || HasProjectHook(root, "unknown") {
		t.Fatal("wrong project hook detection")
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if HasProjectHook(root, "StopFailure") {
		t.Fatal("removed project hook still detected")
	}
}

func TestGlobalSetupUsesQwenHome(t *testing.T) {
	for _, tc := range []struct {
		name, value, subdir string
		wantError           bool
	}{
		{"absolute", "absolute", "custom", false},
		{"tilde", "~/custom", "custom", false},
		{"relative", "relative", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			value := tc.value
			if value == "absolute" {
				value = filepath.Join(home, tc.subdir)
			}
			t.Setenv("QWEN_HOME", value)
			p := filepath.Join(home, tc.subdir, "settings.json")
			if !tc.wantError {
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(`{"model":"keep"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := InstallGlobal(home)
			if (err != nil) != tc.wantError {
				t.Fatalf("install: %v", err)
			}
			if tc.wantError {
				return
			}
			if b, err := os.ReadFile(p); err != nil || !strings.Contains(string(b), " hook qwen-global Stop") {
				t.Fatalf("hooks: %s, %v", b, err)
			}
			if err := UninstallGlobal(home); err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(p); err != nil || !sameJSON(b, []byte(`{"model":"keep"}`)) {
				t.Fatalf("settings changed: %s, %v", b, err)
			}
		})
	}
}

func TestInstallUninstall(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".qwen", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"hooks\":{\"Stop\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"echo keep\"}]}]}}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Install(root); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil || strings.Count(string(b), " hook qwen Stop\"") != 1 || !strings.Contains(string(b), "echo keep") {
		t.Fatalf("installed config = %s, %v", b, err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil || !sameJSON(b, original) {
		t.Fatalf("uninstalled config = %s, %v", b, err)
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
