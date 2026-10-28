package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestCursorContextAndReadPathsSurviveTurnEnd(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UnixMilli()
	base := event.Event{Agent: "cursor", AgentSessionID: "cursor-session", Worktree: root, At: at}
	base.Kind, base.Prompt = event.TurnStart, "fix main"
	if err := e.Process(base); err != nil {
		t.Fatal(err)
	}
	base.Kind, base.Prompt, base.Paths = event.FileRead, "", []string{path, filepath.Join(root, ".env")}
	if err := e.Process(base); err != nil {
		t.Fatal(err)
	}
	base.Kind, base.Paths, base.ContextType, base.Text = event.TurnContext, nil, "thinking", "The guard is needed."
	if err := e.Process(base); err != nil {
		t.Fatal(err)
	}
	base.ContextType, base.Text = "response", "Added it."
	if err := e.Process(base); err != nil {
		t.Fatal(err)
	}
	base.Kind, base.Text, base.ToolName, base.Paths = event.ToolEdit, "", "Write", []string{path}
	if err := e.Process(base); err != nil {
		t.Fatal(err)
	}
	base.Kind, base.ToolName, base.Paths = event.TurnEnd, "", nil
	if err := e.Process(base); err != nil {
		t.Fatal(err)
	}
	var reasoning, tools, files string
	if err := e.DB.QueryRow("SELECT reasoning,tool_calls,files_read FROM turns WHERE seq=1").Scan(&reasoning, &tools, &files); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reasoning, "The guard is needed") || !strings.Contains(reasoning, "Added it") ||
		!strings.Contains(tools, "Write") || files != `["main.go"]` {
		t.Fatalf("reasoning=%q tools=%q files=%q", reasoning, tools, files)
	}
}
