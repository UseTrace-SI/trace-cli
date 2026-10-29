package kiro

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestInstallAndParse(t *testing.T) {
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil || !strings.Contains(string(b), `"trigger": "Stop"`) {
		t.Fatalf("hooks: %s, %v", b, err)
	}
	ev, err := Parse("UserPromptSubmit", []byte(`{"session_id":"s"}`), root, "hello")
	if err != nil || len(ev) != 1 || ev[0].Kind != event.TurnStart || ev[0].Prompt != "hello" || ev[0].Agent != "kiro" {
		t.Fatalf("events: %+v, %v", ev, err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
}

func TestGlobalUpgradeAndProjectOverlap(t *testing.T) {
	home := t.TempDir()
	if err := Install(home); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, relative)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	// Older Trace installs included SessionEnd; accept them for upgrade.
	legacy := append(top["hooks"].([]any), map[string]any{"name": marker + " SessionEnd", "trigger": "SessionEnd", "action": map[string]any{"type": "command", "command": "/old/trace hook kiro SessionEnd"}})
	top["hooks"] = legacy
	legacyBytes, err := json.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacyBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(home); err != nil {
		t.Fatalf("upgrading global hooks: %v", err)
	}
	b, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "hook kiro-global Stop") || strings.Contains(string(b), `"SessionEnd"`) {
		t.Fatalf("global hooks = %s, %v", b, err)
	}
	if HasProjectHook(home, "Stop") {
		t.Fatal("global hook incorrectly treated as a project hook")
	}
	project := t.TempDir()
	if err := Install(project); err != nil {
		t.Fatal(err)
	}
	if !HasProjectHook(project, "Stop") || HasProjectHook(project, "SessionEnd") {
		t.Fatal("project hook detection failed")
	}
	if err := Uninstall(home); err != nil {
		t.Fatal(err)
	}
}

func TestNativeToolEvents(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		kind event.Kind
		path string
	}{
		{"write", `{"session_id":"s","cwd":"/repo/sub","tool_name":"fs_write","tool_input":{"path":"main.go"}}`, event.ToolEdit, "/repo/sub/main.go"},
		{"read", `{"session_id":"s","tool_name":"fs_read","tool_input":{"file_path":"/repo/a.go"}}`, event.FileRead, "/repo/a.go"},
		{"shell", `{"session_id":"s","tool_name":"execute_bash","tool_input":{"command":"secret"}}`, event.ToolEdit, ""},
		{"legacy shell", `{"session_id":"s","tool_name":"execute_cmd","tool_input":{}}`, event.ToolEdit, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse("PostToolUse", []byte(tc.body), "/repo", "")
			if err != nil || len(got) != 1 || got[0].Kind != tc.kind || got[0].Agent != "kiro" {
				t.Fatalf("events: %+v, %v", got, err)
			}
			if tc.path == "" && len(got[0].Paths) != 0 || tc.path != "" && (len(got[0].Paths) != 1 || got[0].Paths[0] != tc.path) {
				t.Fatalf("paths: %+v", got[0].Paths)
			}
			if strings.Contains(got[0].ToolName, "secret") || len(got[0].Raw) != 0 {
				t.Fatalf("raw tool input retained: %+v", got[0])
			}
		})
	}
}

func TestHookIdentityAndPromptFallback(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"session_id":"s","hook_event_name":"PostToolUse","prompt":"wrong"}`,
	} {
		got, err := Parse("UserPromptSubmit", []byte(body), "/repo", "")
		if err != nil || len(got) != 0 {
			t.Fatalf("unexpected events for %s: %+v, %v", body, got, err)
		}
	}
	for _, tc := range []struct {
		name, value, want string
	}{
		{"plain", "fix the file", "fix the file"},
		{"structured prompt", `{"prompt":"fix the file"}`, "fix the file"},
		{"tool envelope", `{"toolName":"fs_write","toolArgs":{"content":"private text"},"toolSuccess":true}`, ""},
		{"arbitrary JSON prompt", `{"question":"what now?"}`, `{"question":"what now?"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse("UserPromptSubmit", []byte(`{"session_id":"s","hook_event_name":"userPromptSubmit"}`), "/repo", tc.value)
			if err != nil || len(got) != 1 || got[0].Prompt != tc.want {
				t.Fatalf("events: %+v, %v", got, err)
			}
		})
	}
}

func TestInstallPreservesEditedHooks(t *testing.T) {
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, relative)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	modified := strings.Replace(string(b), `"version": "v1"`, `"extra": true, "version": "v1"`, 1)
	if err := os.WriteFile(p, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	if Install(root) == nil || Uninstall(root) == nil {
		t.Fatal("modified Kiro hooks were not protected")
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != modified {
		t.Fatalf("modified hooks changed: %v", err)
	}
}

func TestRejectSymlinkedHooks(t *testing.T) {
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, relative)
	target := filepath.Join(root, "user-hooks.json")
	if err := os.Rename(p, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	if Install(root) == nil || Uninstall(root) == nil {
		t.Fatal("accepted symlinked hooks")
	}
}
