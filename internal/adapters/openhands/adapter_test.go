package openhands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestSetupPreservesFormats(t *testing.T) {
	for _, original := range []string{`{"user_prompt_submit":[{"hooks":[{"command":"other"}]}]}`, `{"hooks":{"UserPromptSubmit":[{"hooks":[{"command":"other"}]}]}}`} {
		t.Run(original, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("OH_PERSISTENCE_DIR", dir)
			p := filepath.Join(dir, "hooks.json")
			if err := os.WriteFile(p, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := Setup(t.TempDir(), false); err != nil {
					t.Fatal(err)
				}
			}
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(b), "hook openhands ") != 5 || !strings.Contains(string(b), "other") {
				t.Fatalf("installed: %s", b)
			}
			if err := Setup(t.TempDir(), true); err != nil {
				t.Fatal(err)
			}
			b, err = os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if json.Unmarshal(b, &got) != nil || json.Unmarshal([]byte(original), &want) != nil {
				t.Fatal("invalid JSON")
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("lost user config: %s", b)
			}
		})
	}
}

func TestSetupPersistenceDirectory(t *testing.T) {
	for _, tt := range []struct {
		name, env, subdir string
		wantError         bool
	}{
		{name: "default", subdir: ".openhands"},
		{name: "home expansion", env: "~/custom", subdir: "custom"},
		{name: "relative rejected", env: "relative", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OH_PERSISTENCE_DIR", tt.env)
			if err := Setup(home, false); (err != nil) != tt.wantError {
				t.Fatalf("Setup error: %v", err)
			}
			if tt.wantError {
				return
			}
			path := filepath.Join(home, tt.subdir, "hooks.json")
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("hooks not installed at %s: %v", path, err)
			}
			if err := Setup(home, true); err != nil {
				t.Fatalf("remove hooks: %v", err)
			}
		})
	}
}

func TestProjectHooksPreserved(t *testing.T) {
	for _, original := range []string{`{}`, `{"hooks":{}}`, `{"hooks":{},"note":"keep"}`, `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"user-hook"}]}]}}`, `{"stop":[{"hooks":[{"type":"command","command":"user-hook"}]}]}`} {
		t.Run(original, func(t *testing.T) {
			root := t.TempDir()
			if err := InstallProject(root); err == nil {
				t.Fatal("created project hook file and shadowed global hooks")
			}
			path := filepath.Join(root, ".openhands", "hooks.json")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := InstallProject(root); err != nil {
					t.Fatal(err)
				}
			}
			if err := UninstallProject(root); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("project hooks disappeared: %v", err)
			}
			var a, b any
			if json.Unmarshal([]byte(original), &a) != nil || json.Unmarshal(got, &b) != nil {
				t.Fatal("invalid JSON")
			}
			left, _ := json.Marshal(a)
			right, _ := json.Marshal(b)
			if string(left) != string(right) {
				t.Fatalf("project hooks changed: %s", got)
			}
		})
	}
}

func TestProjectSymlinkPreserved(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".openhands")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "user-hooks.json")
	if err := os.WriteFile(target, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "hooks.json")); err != nil {
		t.Fatal(err)
	}
	if err := InstallProject(root); err == nil {
		t.Fatal("accepted symlinked project hooks")
	}
	if err := UninstallProject(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != `{}` {
		t.Fatalf("symlink target changed: %s, %v", got, err)
	}
}

func TestParse(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		kind       event.Kind
		count      int
	}{
		{"SessionStart", `{"session_id":"s"}`, event.SessionStart, 1},
		{"user_prompt_submit", `{"session_id":"s","message":"fix ghp_abcdefghijklmnopqrstuvwxyz"}`, event.TurnStart, 1},
		{"PostToolUse", `{"session_id":"s","tool_name":"file_editor","tool_input":{"command":"view","path":"a.go"}}`, event.FileRead, 1},
		{"PostToolUse", `{"session_id":"s","tool_name":"file_editor","tool_input":{"command":"create","path":"a.go"}}`, event.ToolEdit, 1},
		{"PostToolUse", `{"session_id":"s","tool_name":"planning_file_editor","tool_input":{"command":"view","path":"a.go"}}`, event.FileRead, 1},
		{"PostToolUse", `{"session_id":"s","tool_name":"planning_file_editor","tool_input":{"command":"str_replace","path":"PLAN.md"}}`, event.ToolEdit, 1},
		{"PostToolUse", `{"session_id":"s","tool_name":"terminal","tool_input":{"command":["echo","hello"],"path":false}}`, event.ToolEdit, 1},
		{"PostToolUse", `{"session_id":"s","tool_name":"file_editor","tool_input":{"command":{"unexpected":true},"path":42}}`, event.ToolEdit, 1},
		{"stop", `{"session_id":"s"}`, event.ToolEdit, 2},
		{"SessionEnd", `{"session_id":"s"}`, event.ToolEdit, 3},
		{"SessionStart", `{}`, "", 0},
		{"SessionStart", `{"session_id":"s","event_type":"Stop"}`, "", 0},
	} {
		t.Run(tt.name+tt.body, func(t *testing.T) {
			got, err := Parse(tt.name, []byte(tt.body), "/repo")
			if err != nil || len(got) != tt.count {
				t.Fatalf("got %#v, %v", got, err)
			}
			if len(got) > 0 && (got[0].Kind != tt.kind || strings.Contains(got[0].Prompt, "ghp_abcdefghijklmnopqrstuvwxyz")) {
				t.Fatalf("event: %#v", got[0])
			}
			if tt.count >= 2 && got[1].Kind != event.TurnEnd {
				t.Fatalf("turn was not closed: %#v", got)
			}
			if tt.count == 3 && got[2].Kind != event.SessionEnd {
				t.Fatalf("session was not closed: %#v", got)
			}
		})
	}
}

func TestUnusableToolFieldsRequestScan(t *testing.T) {
	got, err := Parse("PostToolUse", []byte(`{"session_id":"s","tool_name":"terminal","tool_input":{"command":["edit"],"path":{"bad":true}}}`), "/repo")
	if err != nil || len(got) != 1 || got[0].Kind != event.ToolEdit || len(got[0].Paths) != 0 {
		t.Fatalf("event: %+v, %v", got, err)
	}
}

func TestToolPathUsesWorkingDirectory(t *testing.T) {
	for _, tool := range []string{"file_editor", "planning_file_editor"} {
		got, err := Parse("post_tool_use", []byte(`{"session_id":"s","event_type":"PostToolUse","working_dir":"/repo/sub","tool_name":"`+tool+`","tool_input":{"command":"view","path":"a.go"}}`), "/repo")
		if err != nil || len(got) != 1 || len(got[0].Paths) != 1 || got[0].Paths[0] != "/repo/sub/a.go" {
			t.Fatalf("%s event: %+v, %v", tool, got, err)
		}
	}
}
