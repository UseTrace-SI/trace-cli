package codexhook

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
		{"UserPromptSubmit", `{"session_id":"s","prompt":"do it"}`, event.TurnStart},
		{"PostToolUse", `{"session_id":"s","tool_name":"Edit","tool_input":{"file_path":"x.go"}}`, event.ToolEdit},
		{"SessionEnd", `{"session_id":"s"}`, event.SessionEnd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.name, []byte(tc.body), "/repo")
			if err != nil || len(got) != 1 || got[0].Kind != tc.kind {
				t.Fatalf("Parse = %+v, %v", got, err)
			}
		})
	}
	got, err := Parse("Stop", []byte(`{"session_id":"s","last_assistant_message":"done"}`), "/repo")
	if err != nil || len(got) != 3 || got[0].Kind != event.ToolEdit || got[1].Text != "done" || got[2].Kind != event.TurnEnd {
		t.Fatalf("Stop = %+v, %v", got, err)
	}
	got, err = Parse("Stop", []byte(`{"session_id":"s"}`), "/repo")
	if err != nil || len(got) != 2 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnEnd {
		t.Fatalf("Stop without response = %+v, %v", got, err)
	}
	got, err = Parse("Interrupt", []byte(`{"session_id":"s"}`), "/repo")
	if err != nil || len(got) != 2 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnEnd {
		t.Fatalf("Interrupt = %+v, %v", got, err)
	}
}

func TestInstallUninstall(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Install(home); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, configPath)
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), " hook codex Stop") || !strings.Contains(string(b), " hook codex Interrupt") {
		t.Fatalf("hooks = %s, %v", b, err)
	}
	if err := Uninstall(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("hooks remain: %v", err)
	}
}

func TestSelectedCodexHome(t *testing.T) {
	home, selected := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", selected)
	p := filepath.Join(selected, "hooks.json")
	original := []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo keep"}]}]}}`)
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(home); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "echo keep") || !strings.Contains(string(b), " hook codex Stop") {
		t.Fatalf("hooks: %s %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(home, configPath)); !os.IsNotExist(err) {
		t.Fatalf("default profile changed: %v", err)
	}
	if err := Uninstall(home); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "echo keep") || strings.Contains(string(b), " hook codex ") {
		t.Fatalf("uninstall: %s %v", b, err)
	}
	for _, invalid := range []string{filepath.Join(t.TempDir(), "missing")} {
		t.Setenv("CODEX_HOME", invalid)
		if err := Install(home); err == nil {
			t.Fatalf("accepted invalid home %q", invalid)
		}
	}
}
