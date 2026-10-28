package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestSpoolRoundTripAndTruncation(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		if err := Send(dir, event.Event{Kind: event.TurnStart, At: int64(i + 1), Agent: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	var got []int64
	if err := ingestSpool(dir, func(ev event.Event) { got = append(got, ev.At) }); err != nil {
		t.Fatal(err)
	}
	for i, at := range got {
		if at != int64(i+1) {
			t.Fatalf("event %d out of order: %d", i, at)
		}
	}
	if len(got) != 10 {
		t.Fatalf("processed %d events", len(got))
	}
	got = nil
	if err := ingestSpool(dir, func(ev event.Event) { got = append(got, ev.At) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("spool replayed %d events", len(got))
	}
}

func TestConcurrentSpoolLinesAreWhole(t *testing.T) {
	dir := t.TempDir()
	var group sync.WaitGroup
	for i := 0; i < 40; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			if err := Send(dir, event.Event{Kind: event.TurnStart, At: int64(i + 1), Agent: "test"}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	group.Wait()
	f, err := os.Open(filepath.Join(dir, "spool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	seen := map[int64]bool{}
	for s.Scan() {
		var ev event.Event
		if err := json.Unmarshal(s.Bytes(), &ev); err != nil {
			t.Fatalf("interleaved line: %v", err)
		}
		seen[ev.At] = true
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 40 {
		t.Fatalf("got %d distinct events", len(seen))
	}
}

func TestSpoolDaemonProcessesEvents(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan event.Event, 2)
	done := make(chan error, 1)
	go func() { done <- serveSpool(ctx, dir, func(ev event.Event) { seen <- ev }) }()
	deadline := time.After(2 * time.Second)
	for !SpoolReady(dir) {
		select {
		case <-deadline:
			t.Fatal("spool daemon did not become ready")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	for _, at := range []int64{1, 2} {
		if err := Send(dir, event.Event{Kind: event.TurnStart, Agent: "test", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	for _, at := range []int64{1, 2} {
		select {
		case ev := <-seen:
			if ev.At != at {
				t.Fatalf("event order: got %d want %d", ev.At, at)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("event was not consumed")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("spool daemon did not stop")
	}
}

func TestHookSpoolFallbackP95(t *testing.T) {
	dir := t.TempDir()
	durations := make([]time.Duration, 200)
	for i := range durations {
		start := time.Now()
		if err := Send(dir, event.Event{Kind: event.TurnStart, Agent: "test", At: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
		durations[i] = time.Since(start)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if durations[189] >= 20*time.Millisecond {
		t.Fatalf("spool send p95=%s", durations[189])
	}
}

func TestHookSocketRoundTripP95(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan event.Event, 200)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, dir, func(ev event.Event) { seen <- ev }) }()
	socket := filepath.Join(dir, "sock")
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if _, err := os.Stat(filepath.Join(dir, "spool.ready")); err == nil {
			cancel()
			<-done
			t.Skip("Unix socket binding is unavailable in this sandbox")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatal("daemon socket did not start")
	}
	durations := make([]time.Duration, 200)
	for i := range durations {
		start := time.Now()
		if err := Send(dir, event.Event{Kind: event.TurnStart, Agent: "test", At: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
		durations[i] = time.Since(start)
	}
	for i := 0; i < 200; i++ {
		select {
		case <-seen:
		case <-time.After(2 * time.Second):
			t.Fatal("socket event not consumed")
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if durations[189] >= 20*time.Millisecond {
		t.Fatalf("socket send p95=%s", durations[189])
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
