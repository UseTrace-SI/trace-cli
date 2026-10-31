package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/companions"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestCompanionContextCombinesAndRedacts(t *testing.T) {
	root, bin := t.TempDir(), t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	ev := event.Event{Agent: "codex", AgentSessionID: "context", Worktree: root, At: 1}
	for _, kind := range []event.Kind{event.SessionStart, event.TurnStart} {
		ev.Kind = kind
		ev.Prompt = "authentication change"
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ev.Kind = event.ToolEdit
	ev.Paths = []string{"main.go"}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	ev.Kind = event.TurnEnd
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	writePlugin := func(name string, value any) {
		t.Helper()
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n/bin/cat <<'PAYLOAD'\n"+string(b)+"\nPAYLOAD\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writePlugin("trace-brain", companions.MemoryContext{Protocol: 1, Status: "ready", Memories: []companions.Memory{{ID: "manual", Content: "authentication ghp_abcdefghijklmnopqrstuvwxyz"}}})
	code := companions.CodeContext{Protocol: 1, Status: "ready", Repo: root, Symbols: []companions.Symbol{{Name: "Login", File: "main.go", Line: 1, Signature: "function Login ghp_abcdefghijklmnopqrstuvwxyz"}}, Relations: []companions.Relation{{From: "Login", To: "Validate", File: "main.go", Line: 1}}}
	writePlugin("trace-graph", code)
	t.Setenv("PATH", bin)
	out, err := GetContext(context.Background(), e, ContextInput{Path: "main.go", Query: "authentication"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Steps) != 1 || len(out.Brain.Memories) != 1 || len(out.Graph.Symbols) != 1 || len(out.Graph.Relations) != 1 {
		t.Fatalf("missing context: %+v", out)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "ghp_abcdefghijklmnopqrstuvwxyz") {
		t.Fatal("manual memory or source signature leaked a token")
	}
	if _, err := os.Stat(filepath.Join(root, ".trace", "graph")); !os.IsNotExist(err) {
		t.Fatal("lookup created derived state")
	}
	code.Repo = t.TempDir()
	writePlugin("trace-graph", code)
	out, err = GetContext(context.Background(), e, ContextInput{Path: "main.go"})
	if err != nil || len(out.Steps) != 1 || out.Brain.Status != "ready" || out.Graph.Status != "invalid_response" || len(out.Graph.Symbols) != 0 {
		t.Fatalf("wrong-repo data or lost partial context: %+v %v", out, err)
	}
	for _, path := range []string{"../outside.go", ".env", ".trace-brain/memory.db"} {
		if _, err := GetContext(context.Background(), e, ContextInput{Path: path}); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if _, err := e.DB.Exec("UPDATE turns SET prompt=?", "authentication "+strings.Repeat("x", 5000)); err != nil {
		t.Fatal(err)
	}
	out, err = GetContext(context.Background(), e, ContextInput{Query: "authentication"})
	if err != nil || len(out.Steps) != 1 || len(out.Steps[0].Prompt) > 4000 {
		t.Fatalf("unbounded task history: %+v, %v", out.Steps, err)
	}
}
