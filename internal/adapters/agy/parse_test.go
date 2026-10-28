package agy

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestHookFixtureReplay(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "..", "testdata", "agy", "hooks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := []event.Kind{event.SessionStart, event.TurnStart, event.ToolEdit, event.TurnEnd}
	var got []event.Kind
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row struct {
			Event   string          `json:"event"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		events, err := Parse(row.Event, row.Payload, "/repo")
		if err != nil || len(events) != 1 {
			t.Fatalf("event=%s events=%+v err=%v", row.Event, events, err)
		}
		got = append(got, events[0].Kind)
		if row.Event == "PostToolUse" && (events[0].ToolName != "replace_file_content" || len(events[0].Paths) != 1 || events[0].Paths[0] != "/repo/main.go") {
			t.Fatalf("tool event=%+v", events[0])
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestStopWaitsUntilFullyIdle(t *testing.T) {
	events, err := Parse("Stop", []byte(`{"conversationId":"s","fullyIdle":false}`), "/repo")
	if err != nil || len(events) != 0 {
		t.Fatalf("events=%v err=%v", events, err)
	}
}

func TestTranscriptContext(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "agy", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	prompt, offset, err := LatestPrompt(path)
	if err != nil || prompt != "Update main.go" || offset != 0 {
		t.Fatalf("prompt=%q offset=%d err=%v", prompt, offset, err)
	}
	ctx, next, err := ReadTranscript(path, offset, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Prompt != prompt || !strings.Contains(ctx.Reasoning, "function needs a guard") ||
		!strings.Contains(ctx.Reasoning, "Added the guard") || ctx.FilesRead != `["main.go"]` ||
		len(ctx.WrittenPaths) != 1 || ctx.WrittenPaths[0] != "main.go" ||
		strings.Contains(ctx.ToolCalls, "do not store this") || next != int64(len(source)) {
		t.Fatalf("context=%+v offset=%d", ctx, next)
	}
}
