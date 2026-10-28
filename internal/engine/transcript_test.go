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

func TestClaudeTurnPersistsOnlyNewTranscriptContext(t *testing.T) {
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
	transcript := filepath.Join(root, "session.jsonl")
	old := `{"type":"assistant","message":{"content":[{"type":"text","text":"old turn"}]}}` + "\n"
	if err := os.WriteFile(transcript, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UnixMilli()
	ev := event.Event{Kind: event.TurnStart, Agent: "claude-code", AgentSessionID: "s", Worktree: root, At: at, Prompt: "make a change", TranscriptPath: transcript}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	newLine := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"because test needs it"},{"type":"tool_use","name":"Read","input":{"file_path":"` + filepath.Join(root, "main.go") + `"}}]}}` + "\n"
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(newLine); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	ev.Kind = event.TurnEnd
	ev.Text = "Final answer from Stop"
	ev.At++
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	var reasoning, calls, files string
	if err := e.DB.QueryRow("SELECT reasoning,tool_calls,files_read FROM turns WHERE seq=1").Scan(&reasoning, &calls, &files); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reasoning, "old turn") || !strings.Contains(reasoning, "because test needs it") || !strings.Contains(reasoning, "Final answer from Stop") || !strings.Contains(calls, "Read") || files != `["main.go"]` {
		t.Fatalf("reasoning=%q calls=%q files=%q", reasoning, calls, files)
	}
}
