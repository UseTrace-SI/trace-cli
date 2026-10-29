package factory

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
	}{
		{"SessionStart", `{"session_id":"s","hook_event_name":"SessionStart"}`, event.SessionStart},
		{"UserPromptSubmit", `{"session_id":"s","prompt":"fix it"}`, event.TurnStart},
		{"PostToolUse", `{"session_id":"s","tool_name":"Create","tool_input":{"file_path":"/repo/a.go"}}`, event.ToolEdit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.name, []byte(tc.body), "/repo")
			if err != nil || len(got) != 1 || got[0].Kind != tc.kind || got[0].AgentSessionID != "s" {
				t.Fatalf("Parse() = %+v, %v", got, err)
			}
		})
	}
}

func TestTurnCompletionAndRelativePaths(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		count      int
	}{
		{"Stop", `{"session_id":"s"}`, 2},
		{"Notification", `{"session_id":"s","notification_type":"idle_prompt"}`, 2},
		{"Notification", `{"session_id":"s","notification_type":"permission_prompt"}`, 0},
		{"SessionEnd", `{"session_id":"s"}`, 3},
	} {
		got, err := Parse(tc.name, []byte(tc.body), "/repo")
		if err != nil || len(got) != tc.count {
			t.Fatalf("%s: %+v %v", tc.name, got, err)
		}
		if tc.count == 2 && (got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnEnd) {
			t.Fatalf("completion: %+v", got)
		}
		if tc.count == 3 && (got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnEnd || got[2].Kind != event.SessionEnd) {
			t.Fatalf("session completion: %+v", got)
		}
	}
	got, err := Parse("PostToolUse", []byte(`{"session_id":"s","cwd":"/repo/sub","tool_name":"Edit","tool_input":{"file_path":"a.go"}}`), "/repo")
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0].Paths, []string{"/repo/sub/a.go"}) {
		t.Fatalf("relative path: %+v %v", got, err)
	}
}

func TestSubagentResult(t *testing.T) {
	got, err := Parse("SubagentStop", []byte(`{"session_id":"s","task_result":"done ghp_abcdefghijklmnopqrstuvwxyz"}`), "/repo")
	if err != nil || len(got) != 2 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnContext || strings.Contains(got[1].Text, "ghp_abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("subagent: %+v %v", got, err)
	}
	got, err = Parse("SubagentStop", []byte(`{"session_id":"s","task_result":{"status":"done"}}`), "/repo")
	if err != nil || len(got) != 1 || got[0].Kind != event.ToolEdit {
		t.Fatalf("structured result: %+v %v", got, err)
	}
	if got, err := Parse("SessionEnd", []byte(`{}`), "/repo"); err != nil || len(got) != 0 {
		t.Fatalf("missing session: %+v %v", got, err)
	}
}

func TestGlobalInstallPreservesExistingConfig(t *testing.T) {
	for _, tc := range []struct{ name, path, original string }{
		{"flat", hooksPath, `{"Stop":[{"hooks":[{"type":"command","command":"echo keep"}]}]}`},
		{"settings", settingsPath, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo keep"}]}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, tc.path)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.original), 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := InstallGlobal(home); err != nil {
					t.Fatal(err)
				}
			}
			b, err := os.ReadFile(path)
			if err != nil || strings.Count(string(b), " hook factory-global Stop") != 1 || !strings.Contains(string(b), "echo keep") {
				t.Fatalf("installed: %s %v", b, err)
			}
			if err := UninstallGlobal(home); err != nil {
				t.Fatal(err)
			}
			b, err = os.ReadFile(path)
			if err != nil || !sameJSON(b, []byte(tc.original)) {
				t.Fatalf("removed: %s %v", b, err)
			}
		})
	}
}

func TestLegacyHooksAreNotShadowed(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".factory/hooks/hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(home); err == nil {
		t.Fatal("expected legacy hook migration error")
	}
	if _, err := os.Stat(filepath.Join(home, hooksPath)); !os.IsNotExist(err) {
		t.Fatalf("hooks shadowed legacy file: %v", err)
	}
}

func TestHasProjectHook(t *testing.T) {
	root := t.TempDir()
	if HasProjectHook(root, "Stop") {
		t.Fatal("unexpected hook")
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if !HasProjectHook(root, "Stop") || HasProjectHook(root, "Other") {
		t.Fatal("incorrect project hook detection")
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if HasProjectHook(root, "Stop") {
		t.Fatal("hook still detected")
	}
}

func TestInstallUninstallFlatConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".factory", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"Stop\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"echo keep\"}]}]}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Install(root); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil || strings.Count(string(b), " hook factory Stop") != 1 || !strings.Contains(string(b), "echo keep") || strings.Contains(string(b), `"hooks": {`) {
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

func TestInstallPreservesSettingsHooks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, settingsPath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"hooks\":{\"Stop\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"echo keep\"}]}]}}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, hooksPath)); !os.IsNotExist(err) {
		t.Fatalf("standalone hooks file shadowed settings: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "echo keep") || !strings.Contains(string(b), " hook factory Stop") {
		t.Fatalf("settings hooks missing: %s, %v", b, err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil || !sameJSON(b, original) {
		t.Fatalf("uninstalled settings = %s, %v", b, err)
	}
}

func TestUninstallPreservesLaterFlatConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, hooksPath)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), "{\n", "{\n  \"custom\": \"keep\",\n", 1))
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `"custom": "keep"`) || strings.Contains(string(b), " hook factory ") {
		t.Fatalf("uninstall lost user config: %s, %v", b, err)
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
