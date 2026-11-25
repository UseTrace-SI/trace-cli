package acp

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// TestProxyRecordsTurnInEngine runs the scripted session into a real engine
// and checks the stored turn, its ops, redaction and secret-path exclusion.
func TestProxyRecordsTurnInEngine(t *testing.T) {
	const secret = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	var e *engine.Engine
	sinkErrs := make(chan error, 64)
	h := startProxy(t, "script", func(h *harness) {
		root := h.dir
		if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %s: %v", out, err)
		}
		for name, body := range map[string]string{"main.go": "package main\n", "README.md": "# demo\n"} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var err error
		if e, err = engine.Open(root); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.Close() })
		if err := e.Baseline(); err != nil {
			t.Fatal(err)
		}
		h.prompt = "Fix main.go with token " + secret
		h.sink = func(ev event.Event) {
			if err := e.Process(ev); err != nil {
				sinkErrs <- err
			}
		}
	})
	var processErr error
	h.session()
	_ = h.client.in.Close()
	if err := h.wait(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	close(sinkErrs)
	for err := range sinkErrs {
		processErr = err
	}
	if processErr != nil {
		t.Fatalf("engine: %v", processErr)
	}
	var prompt, reasoning, tools, read string
	var ended sql.NullInt64
	if err := e.DB.QueryRow(`SELECT COALESCE(prompt,''),COALESCE(reasoning,''),COALESCE(tool_calls,''),COALESCE(files_read,''),ended_at FROM turns`).Scan(&prompt, &reasoning, &tools, &read, &ended); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, secret) || !strings.HasPrefix(prompt, "Fix main.go with token") {
		t.Fatalf("prompt = %q", prompt)
	}
	if !ended.Valid || !strings.Contains(reasoning, "Done: main.go now has a main function.") || !strings.Contains(reasoning, "Thinking: The user wants a review.") {
		t.Fatalf("turn = ended %v reasoning %q", ended, reasoning)
	}
	if !strings.Contains(tools, "Editing main.go") || !strings.Contains(read, "README.md") {
		t.Fatalf("tool calls %q, files read %q", tools, read)
	}
	rows, err := e.DB.Query(`SELECT path, actor FROM ops WHERE source='tool' ORDER BY at, path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path, actor string
		if err := rows.Scan(&path, &actor); err != nil {
			t.Fatal(err)
		}
		if actor != "acp:fake" {
			t.Fatalf("actor = %q", actor)
		}
		paths = append(paths, path)
	}
	// The .env edit loses its path to the secret-path filter; the engine
	// records an unknown-path edit and scans the worktree instead.
	got := "," + strings.Join(paths, ",") + ","
	if strings.Contains(got, ".env") || !strings.Contains(got, ",main.go,") || !strings.Contains(got, ",notes.txt,") {
		t.Fatalf("op paths = %q", got)
	}
}
