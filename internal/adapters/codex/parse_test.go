package codex

import (
	"bufio"
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
