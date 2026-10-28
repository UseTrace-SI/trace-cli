package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestConcurrentSessionsMarkLaterEditContested(t *testing.T) {
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
	for _, sid := range []string{"one", "two"} {
		if err := e.Process(event.Event{Kind: event.TurnStart, Agent: "codex", AgentSessionID: sid, Worktree: root, At: at, Prompt: sid}); err != nil {
			t.Fatal(err)
		}
	}
	for i, sid := range []string{"one", "two"} {
		if err := e.Process(event.Event{Kind: event.ToolEdit, Agent: "codex", AgentSessionID: sid, Worktree: root, At: at + int64(i), Paths: []string{path}}); err != nil {
			t.Fatal(err)
		}
	}
	var kind, sessionID string
	if err := e.DB.QueryRow("SELECT kind,session_id FROM ops WHERE path='main.go' ORDER BY at DESC LIMIT 1").Scan(&kind, &sessionID); err != nil {
		t.Fatal(err)
	}
	if kind != "contested" || sessionID == "" {
		t.Fatalf("kind=%q session_id=%q", kind, sessionID)
	}
}
