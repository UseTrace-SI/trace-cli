package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// Without a daemon draining it, the spool must stop growing at its budget.
// Hooks then drop events, count them and still return immediately.
func TestSpoolStopsAtByteLimitAndCountsDrops(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run("overflow="+strconv.FormatBool(overflow), func(t *testing.T) {
			dir := t.TempDir()
			const limit = 2048
			t.Setenv("TRACE_MAX_SPOOL_BYTES", strconv.Itoa(limit))
			if overflow {
				if err := os.WriteFile(filepath.Join(dir, spoolOverflowMarker), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			stored, dropped := 0, 0
			for i := 0; i < 100; i++ {
				start := time.Now()
				err := appendSpool(dir, event.Event{Kind: event.TurnStart, At: int64(i), Prompt: "prompt"})
				if elapsed := time.Since(start); elapsed > time.Second {
					t.Fatalf("append %d took %v", i, elapsed)
				}
				switch {
				case err == nil:
					if dropped > 0 {
						t.Fatalf("event %d stored after an earlier drop", i)
					}
					stored++
				case errors.Is(err, ErrSpoolFull):
					dropped++
				default:
					t.Fatal(err)
				}
			}
			if stored == 0 || dropped == 0 {
				t.Fatalf("stored %d, dropped %d; want both", stored, dropped)
			}
			health := ReadSpoolHealth(dir)
			if health.Bytes > limit || health.Limit != limit {
				t.Fatalf("spool holds %d bytes; limit %d (reported %d)", health.Bytes, limit, health.Limit)
			}
			if health.Dropped != int64(dropped) || health.LastDrop.IsZero() {
				t.Fatalf("health reports %d drops at %v; want %d", health.Dropped, health.LastDrop, dropped)
			}
			// Draining frees the budget; later events are stored again, and every
			// stored event is delivered.
			var got int
			if err := ingestSpoolChecked(dir, func(event.Event) error { got++; return nil }); err != nil {
				t.Fatal(err)
			}
			if got != stored {
				t.Fatalf("delivered %d of %d stored events", got, stored)
			}
			if err := appendSpool(dir, event.Event{Kind: event.TurnEnd, At: 1000}); err != nil {
				t.Fatalf("append after drain: %v", err)
			}
		})
	}
}

func TestOverflowQueueBoundsFileCount(t *testing.T) {
	dir := t.TempDir()
	pending := filepath.Join(dir, "spool-pending")
	if err := os.MkdirAll(pending, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxPendingFrames; i++ {
		if err := os.WriteFile(filepath.Join(pending, strconv.Itoa(i)+".ready"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, spoolOverflowMarker), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := appendSpool(dir, event.Event{Kind: event.TurnStart, At: 1}); !errors.Is(err, ErrSpoolFull) {
		t.Fatalf("append to a full overflow queue: %v; want ErrSpoolFull", err)
	}
	if h := ReadSpoolHealth(dir); h.PendingFiles != maxPendingFrames || h.Dropped != 1 {
		t.Fatalf("health %+v", h)
	}
}

func TestDropCounterSaturates(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, spoolDroppedName), make([]byte, maxDropCounterBytes), 0600); err != nil {
		t.Fatal(err)
	}
	if err := dropFrame(dir); !errors.Is(err, ErrSpoolFull) {
		t.Fatal(err)
	}
	if h := ReadSpoolHealth(dir); h.Dropped != maxDropCounterBytes {
		t.Fatalf("counter grew past its bound: %d", h.Dropped)
	}
}

func TestDropCounterRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(target, filepath.Join(dir, spoolDroppedName)); err != nil {
		t.Fatal(err)
	}
	_ = dropFrame(dir)
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("drop counter was written through a symlink")
	}
}

func TestPendingFrameSizeComesFromTheName(t *testing.T) {
	for name, want := range map[string]int64{
		"00000000000000000001-512-12345.ready":         512,
		"00000000000000000001-512-12345.tmp":           512,
		"00000000000000000001-12345.ready":             0, // named by an older version
		"00000000000000000001-512-12345.ready.invalid": 0, // pruned separately
		"1.ready":                        0,
		"00000000000000000001-x-1.ready": 0,
	} {
		if got := pendingFrameSize(name); got != want {
			t.Errorf("pendingFrameSize(%q) = %d; want %d", name, got, want)
		}
	}
}
