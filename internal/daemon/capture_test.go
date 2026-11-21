package daemon_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/claudecode"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
)

// A Claude Code Stop requests a full scan, and one non-ignored file above
// TRACE_MAX_FILE_BYTES makes that turn_end fail. The daemon must keep
// running, quarantine the frame after a few attempts so an unrelated later
// session is still captured, and never replay events it already processed.
func TestFailingTurnEndDoesNotWedgeCapture(t *testing.T) {
	t.Setenv("TRACE_MAX_FILE_BYTES", "4096")
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".trace")
	hook := func(name, body string) {
		t.Helper()
		events, err := claudecode.ParseEvents(name, []byte(body), root)
		if err != nil {
			t.Fatal(err)
		}
		if err := daemon.SendBatch(dir, events); err != nil {
			t.Fatal(err)
		}
	}
	hook("SessionStart", `{"session_id":"A"}`)
	hook("UserPromptSubmit", `{"session_id":"A","prompt":"edit a"}`)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("two\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hook("PostToolUse", fmt.Sprintf(`{"session_id":"A","tool_name":"Edit","tool_input":{"file_path":%q}}`, filepath.Join(root, "a.txt")))
	if err := os.WriteFile(filepath.Join(root, "big.bin"), []byte(strings.Repeat("x", 8192)), 0600); err != nil {
		t.Fatal(err)
	}
	hook("Stop", `{"session_id":"A"}`)
	hook("SessionStart", `{"session_id":"B"}`)
	hook("UserPromptSubmit", `{"session_id":"B","prompt":"unrelated"}`)

	count := func(query string) int {
		t.Helper()
		var n int
		if err := e.DB.QueryRow(query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	editOps := func() int { return count("SELECT COUNT(*) FROM ops WHERE path='a.txt' AND source='tool'") }
	sessionB := func() int {
		return count("SELECT COUNT(*) FROM turns t JOIN sessions s ON s.id=t.session_id WHERE s.agent_sid='B'")
	}
	serve := func(until func() bool) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- daemon.ServeChecked(ctx, dir, e.Process) }()
		for deadline := time.Now().Add(10 * time.Second); !until(); time.Sleep(20 * time.Millisecond) {
			select {
			case err := <-done:
				t.Fatalf("daemon stopped on a processing failure: %v", err)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatal("events after the failing frame were not processed")
			}
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("stopping daemon: %v", err)
		}
	}

	serve(func() bool { return sessionB() > 0 })
	if got := editOps(); got != 1 {
		t.Fatalf("a.txt tool edit recorded %d times, want 1", got)
	}
	files, err := filepath.Glob(filepath.Join(dir, "quarantine", "*.frame"))
	if err != nil || len(files) != 1 {
		t.Fatalf("quarantined frames = %v, %v; want the failing turn_end", files, err)
	}
	if data, err := os.ReadFile(files[0]); err != nil || !strings.Contains(string(data), "TRACE_MAX_FILE_BYTES") || !strings.Contains(string(data), `"turn_end"`) {
		t.Fatalf("quarantined frame lacks the turn_end or its error: %s, %v", data, err)
	}

	// A restarted daemon has nothing left to replay.
	started := time.Now()
	serve(func() bool { return time.Since(started) > 300*time.Millisecond })
	if got, sessions := editOps(), sessionB(); got != 1 || sessions != 1 {
		t.Fatalf("after restart: a.txt edits %d, session B turns %d; want 1 and 1", got, sessions)
	}
}
