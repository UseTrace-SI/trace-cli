package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// BenchmarkOverflowAppend4000Pending measures one hook append while a stuck
// overflow queue already holds 4000 frames.
func BenchmarkOverflowAppend4000Pending(b *testing.B) {
	dir := b.TempDir()
	pending := filepath.Join(dir, "spool-pending")
	if err := os.MkdirAll(pending, 0700); err != nil {
		b.Fatal(err)
	}
	frame := []byte(`{"kind":"turn_start","at":1}` + "\n")
	for i := 0; i < 4000; i++ {
		if err := os.WriteFile(filepath.Join(pending, fmt.Sprintf("%020d-%d-%d.ready", i, len(frame), i)), frame, 0600); err != nil {
			b.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, spoolOverflowMarker), nil, 0600); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Keep the queue at 4000 frames.
		entries, _ := os.ReadDir(pending)
		for _, e := range entries[4000:] {
			_ = os.Remove(filepath.Join(pending, e.Name()))
		}
		b.StartTimer()
		if err := appendSpool(dir, event.Event{Kind: event.TurnStart, At: int64(i)}); err != nil {
			b.Fatal(err)
		}
	}
}
