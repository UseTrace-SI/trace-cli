package goose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kind       event.Kind
	}{
		{"SessionStart", `{"session_id":"s"}`, event.SessionStart},
		{"UserPromptSubmit", `{"session_id":"s","message":"do it"}`, event.TurnStart},
		{"PostToolUse", `{"session_id":"s","tool_name":"write","tool_input":{"path":"x.go"}}`, event.ToolEdit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.name, []byte(tc.body), "/repo")
			if err != nil || len(got) != 1 || got[0].Kind != tc.kind {
				t.Fatalf("Parse = %+v, %v", got, err)
			}
		})
	}
}

func TestCompletionAndNamespacedTools(t *testing.T) {
	got, err := Parse("Stop", []byte(`{"event":"Stop","session_id":"s","last_assistant_message":"done"}`), "/repo")
	if err != nil || len(got) != 3 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnContext || got[1].Text != "done" || got[2].Kind != event.TurnEnd {
		t.Fatalf("Stop = %+v, %v", got, err)
	}
	got, err = Parse("Stop", []byte(`{"session_id":"s","last_assistant_message":"token sk-abcdefghijklmnopqrstuvwxyz1234567890"}`), "/repo")
	if err != nil || len(got) != 3 || strings.Contains(got[1].Text, "sk-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("response was not redacted: %+v, %v", got, err)
	}
	got, err = Parse("SessionEnd", []byte(`{"session_id":"s"}`), "/repo")
	if err != nil || len(got) != 3 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnEnd || got[2].Kind != event.SessionEnd {
		t.Fatalf("SessionEnd = %+v, %v", got, err)
	}
	got, err = Parse("PostToolUse", []byte(`{"session_id":"s","tool_name":"developer__text_editor","working_dir":"/repo/sub","tool_input":{"path":"x.go"}}`), "/repo")
	if err != nil || len(got) != 1 || got[0].Kind != event.ToolEdit || len(got[0].Paths) != 1 || got[0].Paths[0] != "/repo/sub/x.go" {
		t.Fatalf("tool = %+v, %v", got, err)
	}
	for _, body := range []string{`{"event":"Stop","session_id":"s"}`, `{"message":"hello"}`, `{"session_id":"s","tool_name":"other__write"}`} {
		got, err = Parse("PostToolUse", []byte(body), "/repo")
		if err != nil || len(got) != 0 {
			t.Fatalf("unexpected events for %s: %+v, %v", body, got, err)
		}
	}
}

func TestPreserveModifiedManifest(t *testing.T) {
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, manifest)
	modified := `{"name":"trace","version":"0.1.0","description":"Trace hook bridge for goose","user_setting":true}`
	if err := os.WriteFile(path, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("install should refuse modified manifest")
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != modified {
		t.Fatalf("manifest changed: %s, %v", b, err)
	}
}

func TestInstallUninstall(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, hooksPath))
	if err != nil || !strings.Contains(string(b), " hook goose Stop") {
		t.Fatalf("hooks = %s, %v", b, err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, manifest)); !os.IsNotExist(err) {
		t.Fatalf("manifest remains: %v", err)
	}
}
