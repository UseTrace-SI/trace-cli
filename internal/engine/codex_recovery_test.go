package engine

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/codex"
)

// Replay the observed setup-before-prompt shape through the real persisted
// cursor and engine, then interrupt/resume after reopening both components.
func TestCodexRolloutPromptAndInterruptedResumeAfterRestart(t *testing.T) {
	root, transcripts := t.TempDir(), t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	file := filepath.Join(root, "acceptance.txt")
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("before\n")
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if check() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("rollout replay did not reach expected state")
	}
	start := func() func() {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- codex.Run(ctx, root, transcripts, e.Process) }()
		return func() {
			cancel()
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}
	stop := start()
	t.Cleanup(func() {
		if stop != nil {
			stop()
		}
		e.Close()
	})
	cursor := filepath.Join(root, ".trace", "codex-offsets.json")
	wait(func() bool { _, err := os.Stat(cursor); return err == nil })
	rollout := filepath.Join(transcripts, "fixture.jsonl")
	appendRecord := func(kind string, payload any) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"type": kind, "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(rollout, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Write(append(data, '\n'))
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("writing fixture: %v, %v", err, closeErr)
		}
	}
	prompt := func(text string) {
		appendRecord("response_item", map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": text}}})
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := e.DB.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	appendRecord("session_meta", map[string]string{"id": "fixture-thread", "cwd": root})
	prompt("# AGENTS.md instructions\n<INSTRUCTIONS>fixture rules</INSTRUCTIONS><environment_context>fixture</environment_context>")
	prompt("first actual request")
	appendRecord("response_item", map[string]string{"type": "custom_tool_call", "name": "exec"})
	write("one\n")
	wait(func() bool {
		return count("SELECT count(*) FROM turns WHERE prompt=? AND ended_at IS NULL", "first actual request") == 1
	})
	// Wait for the accepted events' persisted byte cursor before restarting.
	wait(func() bool {
		data, err := os.ReadFile(cursor)
		if err != nil {
			return false
		}
		var positions map[string]struct {
			Offset int64 `json:"offset"`
		}
		if json.Unmarshal(data, &positions) != nil {
			return false
		}
		info, err := os.Stat(rollout)
		return err == nil && positions[rollout].Offset == info.Size()
	})
	stop()
	stop = nil
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	e = reopened
	stop = start()
	appendRecord("event_msg", map[string]string{"type": "turn_aborted", "reason": "interrupted"})
	prompt("<turn_aborted>interrupted by owner</turn_aborted>")
	wait(func() bool { return count("SELECT count(*) FROM turns WHERE ended_at IS NOT NULL") == 1 })
	prompt("resumed actual request")
	wait(func() bool {
		return count("SELECT count(*) FROM turns WHERE prompt=? AND ended_at IS NULL", "resumed actual request") == 1
	})
	write("two\n")
	appendRecord("response_item", map[string]string{"type": "custom_tool_call", "name": "exec"})
	appendRecord("event_msg", map[string]string{"type": "task_complete"})
	wait(func() bool { return count("SELECT count(*) FROM keyframes WHERE seq>0") == 2 })
	stop()
	stop = nil
	if count("SELECT count(*) FROM sessions WHERE agent_sid='fixture-thread'") != 1 || count("SELECT count(*) FROM turns") != 2 || count("SELECT count(*) FROM turns WHERE ended_at IS NULL") != 0 {
		t.Fatal("resume duplicated sessions/turns or retained an open interrupted turn")
	}
	write("verification overwrite\n")
	if _, _, err := e.Rewind(2, "acceptance.txt", false); err != nil {
		t.Fatal(err)
	}
	if bytes, err := os.ReadFile(file); err != nil || string(bytes) != "two\n" {
		t.Fatalf("resumed keyframe = %q, %v", bytes, err)
	}
}
