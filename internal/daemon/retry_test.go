package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// After a processing failure the spool keeps only the failed event and the
// ones after it, so a retry does not repeat events that were processed,
// including earlier events of the same batch.
func TestProcessingFailureKeepsOnlyUnprocessedEvents(t *testing.T) {
	frames := []string{
		`{"kind":"turn_start","agent":"test","at":1}`,
		`{"events":[{"kind":"tool_edit","agent":"test","at":2},{"kind":"turn_context","agent":"test","at":3},{"kind":"turn_end","agent":"test","at":4}]}`,
		`{"kind":"session_end","agent":"test","at":5}`,
	}
	for _, tc := range []struct {
		name  string
		spool func(t *testing.T, dir string)
	}{
		{"spool", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "spool.jsonl"), strings.Join(frames, "\n")+"\n")
		}},
		{"overflow", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, spoolOverflowMarker), "")
			for i, frame := range frames {
				writeFile(t, filepath.Join(dir, "spool-pending", fmt.Sprintf("%020d-x.ready", i+1)), frame+"\n")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.spool(t, dir)
			var first []int64
			err := ingestSpoolChecked(dir, func(ev event.Event) error {
				first = append(first, ev.At)
				if ev.At == 3 {
					return errors.New("engine failure")
				}
				return nil
			})
			if err == nil {
				t.Fatal("processing failure was ignored")
			}
			if fmt.Sprint(first) != "[1 2 3]" {
				t.Fatalf("first attempt delivered %v, want [1 2 3]", first)
			}
			var retry []int64
			if err := ingestSpoolChecked(dir, func(ev event.Event) error { retry = append(retry, ev.At); return nil }); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(retry) != "[3 4 5]" {
				t.Fatalf("retry delivered %v, want [3 4 5]: processed events were replayed", retry)
			}
		})
	}
}

// One event the engine cannot process must neither stop the daemon nor block
// later events: it is retried with backoff, then quarantined with its
// redacted error, and capture continues.
func TestRepeatedProcessingFailureIsQuarantined(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz012345"
	for _, tc := range []struct {
		name string
		dir  func(t *testing.T) string
	}{
		{"socket daemon", func(t *testing.T) string {
			dir, err := os.MkdirTemp("", "tr-retry-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			return dir
		}},
		// A socket path this long cannot be bound, so the daemon polls the spool.
		{"spool daemon", func(t *testing.T) string { return filepath.Join(t.TempDir(), strings.Repeat("x", 120)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.dir(t)
			for at := int64(1); at <= 3; at++ {
				if err := appendSpool(dir, event.Event{Kind: event.TurnEnd, Agent: "test", At: at}); err != nil {
					t.Fatal(err)
				}
			}
			var mu sync.Mutex
			attempts := map[int64]int{}
			consume := func(ev event.Event) error {
				mu.Lock()
				defer mu.Unlock()
				attempts[ev.At]++
				if ev.At == 2 {
					return fmt.Errorf("capture file big.bin exceeds TRACE_MAX_FILE_BYTES (token %s)", secret)
				}
				return nil
			}
			logs := &syncBuffer{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- ServeCheckedWithLogger(ctx, dir, slog.New(slog.NewTextHandler(logs, nil)), consume) }()
			delivered := func() bool { mu.Lock(); defer mu.Unlock(); return attempts[3] > 0 }
			for deadline := time.Now().Add(5 * time.Second); !delivered(); time.Sleep(10 * time.Millisecond) {
				select {
				case err := <-done:
					t.Fatalf("daemon stopped on a processing failure: %v", err)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("the event after a failing frame was never processed")
				}
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("stopping daemon: %v", err)
			}
			mu.Lock()
			got := fmt.Sprint(attempts)
			mu.Unlock()
			if want := fmt.Sprint(map[int64]int{1: 1, 2: maxFrameAttempts, 3: 1}); got != want {
				t.Fatalf("processing attempts %s, want %s", got, want)
			}
			files, err := filepath.Glob(filepath.Join(dir, "quarantine", "*.frame"))
			if err != nil || len(files) != 1 {
				t.Fatalf("quarantined frames = %v, %v; want 1", files, err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "TRACE_MAX_FILE_BYTES") || strings.Contains(string(data), secret) {
				t.Fatalf("quarantined frame must keep the redacted error: %s", data)
			}
			if events, err := decodeFrame(data); err != nil || len(events) != 1 || events[0].At != 2 {
				t.Fatalf("quarantined frame is not the failing event: %+v, %v", events, err)
			}
			if strings.Contains(logs.String(), secret) {
				t.Fatalf("secret leaked into daemon log: %s", logs.String())
			}
			if b, err := os.ReadFile(filepath.Join(dir, "spool.jsonl")); err != nil || len(b) != 0 {
				t.Fatalf("spool still holds frames: %q, %v", b, err)
			}
		})
	}
}
