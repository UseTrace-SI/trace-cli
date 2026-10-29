package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestCodexSourcesPreferHooksPerSession(t *testing.T) {
	var got []event.Event
	s := &codexSources{process: func(ev event.Event) error { got = append(got, ev); return nil }}
	hook := event.Event{Agent: "codex", AgentSessionID: "hooked", Kind: event.SessionStart}
	if err := s.hook(hook); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []event.Kind{event.TurnStart, event.ToolEdit, event.TurnEnd, event.TurnContext} {
		hook.Kind = kind
		if err := s.hook(hook); err != nil {
			t.Fatal(err)
		}
	}
	got = nil
	for _, ev := range []event.Event{
		{Agent: "codex", AgentSessionID: "hooked", Kind: event.TurnStart},
		{Agent: "codex", AgentSessionID: "hooked", Kind: event.ToolEdit},
		{Agent: "codex", AgentSessionID: "hooked", Kind: event.TurnEnd},
		{Agent: "codex", AgentSessionID: "hooked", Kind: event.TurnContext, ContextType: "response"},
		{Agent: "codex", AgentSessionID: "hooked", Kind: event.TurnContext, ContextType: "thinking"},
		{Agent: "codex", AgentSessionID: "transcript-only", Kind: event.TurnStart},
	} {
		if err := s.transcript(ev); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 3 || got[0].ContextType != "response" || got[1].ContextType != "thinking" || got[2].AgentSessionID != "transcript-only" {
		t.Fatalf("events: %+v", got)
	}
}

func TestCodexSourcesAllowUnobservedHookKinds(t *testing.T) {
	var got []event.Event
	s := &codexSources{process: func(ev event.Event) error { got = append(got, ev); return nil }}
	ev := event.Event{Agent: "codex", AgentSessionID: "s", Kind: event.SessionStart}
	if err := s.hook(ev); err != nil {
		t.Fatal(err)
	}
	ev.Kind = event.TurnStart
	if err := s.transcript(ev); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Kind != event.TurnStart {
		t.Fatalf("partial trust lost transcript fallback: %+v", got)
	}
}

func TestCodexSourcesPreserveUnobservedTools(t *testing.T) {
	var got []event.Event
	s := &codexSources{process: func(ev event.Event) error { got = append(got, ev); return nil }}
	ev := event.Event{Agent: "codex", AgentSessionID: "s", Kind: event.ToolEdit, ToolName: "exec_command"}
	if err := s.hook(ev); err != nil {
		t.Fatal(err)
	}
	if err := s.transcript(ev); err != nil {
		t.Fatal(err)
	}
	ev.ToolName = "custom_tool"
	if err := s.transcript(ev); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ToolName != "custom_tool" {
		t.Fatalf("unobserved tool lost: %+v", got)
	}
}

func TestFailedHookLeavesTranscriptFallback(t *testing.T) {
	failed := true
	count := 0
	s := &codexSources{process: func(event.Event) error {
		if failed {
			failed = false
			return errors.New("failure")
		}
		count++
		return nil
	}}
	ev := event.Event{Agent: "codex", AgentSessionID: "s", Kind: event.TurnStart}
	if err := s.hook(ev); err == nil {
		t.Fatal("expected hook failure")
	}
	if err := s.transcript(ev); err != nil || count != 1 {
		t.Fatalf("transcript fallback: %d %v", count, err)
	}
}

func TestCodexSourcesDoNotDuplicateTurn(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	path := filepath.Join(root, "main.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
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
	s := &codexSources{process: e.Process}
	ev := event.Event{Agent: "codex", AgentSessionID: "s", Worktree: root, At: time.Now().UnixMilli(), Kind: event.SessionStart}
	if err := s.hook(ev); err != nil {
		t.Fatal(err)
	}
	ev.Kind, ev.Prompt = event.TurnStart, "fix"
	if err := s.hook(ev); err != nil {
		t.Fatal(err)
	}
	if err := s.transcript(ev); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	ev.Kind, ev.Prompt, ev.Paths = event.ToolEdit, "", []string{path}
	if err := s.hook(ev); err != nil {
		t.Fatal(err)
	}
	if err := s.transcript(ev); err != nil {
		t.Fatal(err)
	}
	ev.Kind, ev.Paths = event.TurnEnd, nil
	if err := s.hook(ev); err != nil {
		t.Fatal(err)
	}
	if err := s.transcript(ev); err != nil {
		t.Fatal(err)
	}
	var turns, operations int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM turns").Scan(&turns); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM ops WHERE source='tool' AND start_line IS NULL").Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if turns != 1 || operations != 1 {
		t.Fatalf("turns=%d tool operations=%d", turns, operations)
	}
}
