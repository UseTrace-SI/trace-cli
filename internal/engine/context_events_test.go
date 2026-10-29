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
	var assistant string
	if err := e.DB.QueryRow("SELECT content FROM messages WHERE role='assistant'").Scan(&assistant); err != nil {
		t.Fatalf("assistant message missing: %v", err)
	}
	if assistant != reasoning {
		t.Fatalf("assistant message = %q; want %q", assistant, reasoning)
	}
}

func TestMergeTurnContext(t *testing.T) {
	for _, tt := range []struct{ recorded, transcript, want string }{
		{"", "transcript", "transcript"},
		{"hook", "", "hook"},
		{"hook", "hook", "hook"},
		{"hook", "transcript contains hook", "transcript contains hook"},
		{"hook context", "transcript context", "hook context\n\ntranscript context"},
	} {
		if got := mergeTurnContext(tt.recorded, tt.transcript); got != tt.want {
			t.Errorf("mergeTurnContext(%q, %q) = %q, want %q", tt.recorded, tt.transcript, got, tt.want)
		}
	}
}

func TestAssistantMessageFailureDoesNotFinishTurn(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	ev := event.Event{Agent: "test", AgentSessionID: "s", Worktree: root, At: time.Now().UnixMilli(), Kind: event.TurnStart, Prompt: "hello"}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	ev.Kind, ev.Text, ev.ContextType = event.TurnContext, "answer", "response"
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec(`CREATE TRIGGER reject_assistant BEFORE INSERT ON messages WHEN NEW.role='assistant' BEGIN SELECT RAISE(ABORT, 'test'); END`); err != nil {
		t.Fatal(err)
	}
	ev.Kind = event.TurnEnd
	if err := e.Process(ev); err == nil {
		t.Fatal("expected assistant message failure")
	}
	var ended int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM turns WHERE ended_at IS NOT NULL").Scan(&ended); err != nil || ended != 0 {
		t.Fatalf("turn ended after failure: %d, %v", ended, err)
	}
	if _, err := e.DB.Exec("DROP TRIGGER reject_assistant"); err != nil {
		t.Fatal(err)
	}
	if err := e.Process(ev); err != nil {
		t.Fatalf("retry: %v", err)
	}
	var count int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM messages WHERE role='assistant'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("assistant messages: %d, %v", count, err)
	}
}
