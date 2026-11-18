package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func openTestTurn(t *testing.T, e *Engine, agent string) event.Event {
	t.Helper()
	ev := event.Event{Kind: event.TurnStart, Agent: agent, AgentSessionID: "s", Worktree: e.Root, At: time.Now().UnixMilli(), Prompt: "test turn"}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestAppendToolCallBoundsArray(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra int
	}{
		{"at limit", 0},
		{"over limit", 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			e, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			_ = openTestTurn(t, e, "codex")
			var turnID string
			if err := e.DB.QueryRow("SELECT id FROM turns WHERE ended_at IS NULL ORDER BY started_at DESC LIMIT 1").Scan(&turnID); err != nil {
				t.Fatal(err)
			}
			total := maxTurnToolCalls + tc.extra
			for i := 0; i < total; i++ {
				if err := e.appendToolCall(turnID, fmt.Sprintf("tool_%03d", i), ""); err != nil {
					t.Fatal(err)
				}
			}
			var raw string
			if err := e.DB.QueryRow("SELECT tool_calls FROM turns WHERE id=?", turnID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var calls []toolCallSummary
			if err := json.Unmarshal([]byte(raw), &calls); err != nil {
				t.Fatal(err)
			}
			want := total
			if tc.extra > 0 {
				want = maxTurnToolCalls + 1
				got := droppedToolCalls(calls)
				if got != tc.extra {
					t.Fatalf("overflow count = %d, want %d", got, tc.extra)
				}
			} else if droppedToolCalls(calls) >= 0 {
				t.Fatal("unexpected overflow marker below the limit")
			}
			if len(calls) != want {
				t.Fatalf("stored %d tool calls, want %d", len(calls), want)
			}
		})
	}
}

func TestFileReadBoundsArray(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra int
	}{
		{"at limit", 0},
		{"over limit", 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			e, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			ev := openTestTurn(t, e, "codex")
			total := maxTurnFilesRead + tc.extra
			ev.Kind, ev.Paths = event.FileRead, nil
			for i := 0; i < total; i++ {
				path := filepath.Join(root, fmt.Sprintf("file_%03d.txt", i))
				if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
				ev.Paths = append(ev.Paths, path)
			}
			// A duplicate must not count towards the cap.
			ev.Paths = append(ev.Paths, ev.Paths[0])
			if err := e.fileRead(ev); err != nil {
				t.Fatal(err)
			}
			var raw string
			if err := e.DB.QueryRow("SELECT files_read FROM turns WHERE seq=1").Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var files []string
			if err := json.Unmarshal([]byte(raw), &files); err != nil {
				t.Fatal(err)
			}
			want := total
			if tc.extra > 0 {
				want = maxTurnFilesRead + 1
				marker := files[len(files)-1]
				if !filepath.IsAbs(marker) && marker == fmt.Sprintf("%s%d%s", filesOverflowPrefix, tc.extra, filesOverflowSuffix) {
					// expected marker
				} else {
					t.Fatalf("wrong overflow marker: %q", marker)
				}
			}
			if len(files) != want {
				t.Fatalf("stored %d read paths, want %d", len(files), want)
			}
			parsed, overflow := splitFilesOverflow(files)
			if overflow != tc.extra || len(parsed) != min(total, maxTurnFilesRead) {
				t.Fatalf("parsed %d paths with overflow %d", len(parsed), overflow)
			}
		})
	}
}
