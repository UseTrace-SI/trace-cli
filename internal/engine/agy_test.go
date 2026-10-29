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

func TestAgyTranscriptCompletesTurnAndRecoversToolEdit(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0600); err != nil {
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
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	user := `{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>Add a guard</USER_REQUEST>"}` + "\n"
	if err := os.WriteFile(transcript, []byte(user), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UnixMilli()
	ev := event.Event{Kind: event.TurnStart, Agent: "agy", AgentSessionID: "conversation", Worktree: root, At: at, TranscriptPath: transcript}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package main\nfunc guard() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	planner := `{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","thinking":"The guard belongs here.","content":"Added it.","tool_calls":[{"name":"view_file","args":{"AbsolutePath":"` + file + `"}},{"name":"replace_file_content","args":{"TargetFile":"` + file + `","ReplacementContent":"not saved"}}]}` + "\n"
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(planner); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	ev.Kind = event.TurnEnd
	ev.At++
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	var prompt, reasoning, calls, reads string
	if err := e.DB.QueryRow("SELECT prompt,reasoning,tool_calls,files_read FROM turns WHERE seq=1").Scan(&prompt, &reasoning, &calls, &reads); err != nil {
		t.Fatal(err)
	}
	if prompt != "Add a guard" || !strings.Contains(reasoning, "guard belongs here") ||
		!strings.Contains(calls, "replace_file_content") || strings.Contains(calls, "not saved") || reads != `["main.go"]` {
		t.Fatalf("prompt=%q reasoning=%q calls=%q reads=%q", prompt, reasoning, calls, reads)
	}
	var n int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM ops WHERE path='main.go' AND turn_id IS NOT NULL AND source='tool'").Scan(&n); err != nil || n == 0 {
		t.Fatalf("tool edit not recovered: %d, %v", n, err)
	}
	baseline, err := e.ManifestAt(0)
	if err != nil {
		t.Fatal(err)
	}
	current, err := e.ManifestAt(1)
	if err != nil {
		t.Fatal(err)
	}
	if baseline["main.go"].Hash == current["main.go"].Hash {
		t.Fatal("Agy keyframe missed changed file")
	}
}

func TestAgyLateTranscriptPromptIsRecordedAsMessage(t *testing.T) {
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
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, nil, 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UnixMilli()
	ev := event.Event{Kind: event.TurnStart, Agent: "agy", AgentSessionID: "s", Worktree: root, At: at, TranscriptPath: transcript}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	line := `{"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>Fix ` + secret + `</USER_REQUEST>"}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	ev.Kind, ev.At = event.TurnEnd, at+1
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	var prompt, message string
	if err := e.DB.QueryRow(`SELECT t.prompt,m.content FROM turns t JOIN messages m ON m.turn_id=t.id WHERE m.role='user'`).Scan(&prompt, &message); err != nil {
		t.Fatal(err)
	}
	if prompt != message || strings.Contains(message, secret) || !strings.Contains(message, "redacted") {
		t.Fatalf("prompt=%q message=%q", prompt, message)
	}
}
