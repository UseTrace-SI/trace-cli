package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestFixtureReplay(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "..", "testdata", "codex", "rollout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p := Parser{Root: "/repo"}
	var got []event.Kind
	s := bufio.NewScanner(f)
	for s.Scan() {
		events, err := p.Feed(s.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range events {
			got = append(got, ev.Kind)
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	want := []event.Kind{event.SessionStart, event.TurnStart, event.ToolEdit, event.TurnContext, event.TurnContext, event.TurnEnd}
	if len(got) != len(want) {
		t.Fatalf("events: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events: %v", got)
		}
	}
}

func TestSessionDirectoryResolvesWorktree(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{filepath.Join(root, ".git"), filepath.Join(root, "nested"), filepath.Join(root, "other", ".git")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{filepath.Join(root, ".git"), filepath.Join(root, "other", ".git")} {
		if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		cwd    string
		active bool
	}{{root, true}, {filepath.Join(root, "nested"), true}, {filepath.Join(root, "other"), false}} {
		p := Parser{Root: root}
		body, err := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": "s", "cwd": tc.cwd}})
		if err != nil {
			t.Fatal(err)
		}
		events, err := p.Feed(body)
		if err != nil || p.Active != tc.active || (len(events) == 1) != tc.active {
			t.Fatalf("cwd %s: active=%t events=%v err=%v", tc.cwd, p.Active, events, err)
		}
	}
}
