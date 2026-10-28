//go:build treesitter

package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestStructuralOpStoredWithTurn(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\nfunc Old() int { return 1 }\n"), 0600); err != nil {
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
	ev := event.Event{Kind: event.TurnStart, Agent: "codex", AgentSessionID: "test", Worktree: root, At: time.Now().UnixMilli()}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package main\nfunc New() int { return 1 }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordChanges([]string{path}); err != nil {
		t.Fatal(err)
	}
	var kind, detail string
	if err := e.DB.QueryRow("SELECT kind,detail FROM ops WHERE kind='function-renamed' LIMIT 1").Scan(&kind, &detail); err != nil {
		t.Fatal(err)
	}
	if kind != "function-renamed" || detail != "Old → New" {
		t.Fatalf("structural op: %s %q", kind, detail)
	}
}
