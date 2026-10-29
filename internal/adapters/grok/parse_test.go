package grok

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
		{"SessionStart", `{"sessionId":"s"}`, event.SessionStart},
		{"UserPromptSubmit", `{"sessionId":"s","prompt":"build"}`, event.TurnStart},
		{"PostToolUse", `{"sessionId":"s","toolName":"edit","toolInput":{"path":"main.go"}}`, event.ToolEdit},
		{"Stop", `{"sessionId":"s"}`, event.TurnEnd},
		{"SessionEnd", `{"sessionId":"s"}`, event.SessionEnd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.name, []byte(tc.body), "/repo")
			if err != nil || len(got) != 1 || got[0].Kind != tc.kind {
				t.Fatalf("Parse = %+v, %v", got, err)
			}
		})
	}
	got, err := Parse("Stop", []byte(`{"sessionId":"s","lastAssistantMessage":"done"}`), "/repo")
	if err != nil || len(got) != 2 || got[0].Text != "done" || got[1].Kind != event.TurnEnd {
		t.Fatalf("Stop = %+v, %v", got, err)
	}
	got, err = Parse("Stop", []byte(`{"subagentType":"test"}`), "/repo")
	if err != nil || len(got) != 0 {
		t.Fatalf("subagent = %+v, %v", got, err)
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
	path := filepath.Join(root, hooksPath)
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), " hook grok Stop") {
		t.Fatalf("hooks = %s, %v", b, err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("hooks remain: %v", err)
	}
}
