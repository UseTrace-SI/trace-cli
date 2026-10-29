package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestSpoolRetainsFailedFrames(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(strconv.FormatBool(overflow), func(t *testing.T) {
			dir := t.TempDir()
			if overflow {
				if err := os.WriteFile(filepath.Join(dir, "spool-overflow"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := appendSpool(dir, event.Event{Kind: event.TurnStart, At: 1}); err != nil {
				t.Fatal(err)
			}
			if err := ingestSpoolChecked(dir, func(event.Event) error { return errors.New("temporary failure") }); err == nil {
				t.Fatal("processing failure was ignored")
			}
			var got []event.Event
			if err := ingestSpoolChecked(dir, func(ev event.Event) error { got = append(got, ev); return nil }); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Kind != event.TurnStart {
				t.Fatalf("retry lost event: %+v", got)
			}
			got = nil
			if err := ingestSpoolChecked(dir, func(ev event.Event) error { got = append(got, ev); return nil }); err != nil || len(got) != 0 {
				t.Fatalf("successful replay retained event: %+v, %v", got, err)
			}
		})
	}
}

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

func TestBatchSpoolRedactsAndPreservesOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	cleanup := markBatchReady(dir)
	defer cleanup()
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	events := []event.Event{
		{Kind: event.ToolEdit, Agent: "copilot", At: 1, Paths: []string{".env"}},
		{Kind: event.TurnContext, Agent: "copilot", AgentSessionID: secret, Worktree: "/repo/" + secret, At: 2, Text: "message " + secret},
		{Kind: event.TurnEnd, Agent: "copilot", At: 3},
	}
	if err := SendBatch(dir, events); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "spool.jsonl"))
	if err != nil || strings.Count(string(b), "\n") != 1 || strings.Contains(string(b), secret) || strings.Contains(string(b), ".env") {
		t.Fatalf("batch was not one redacted line: %s %v", b, err)
	}
	var got []event.Event
	if err := ingestSpool(dir, func(ev event.Event) { got = append(got, ev) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnContext || got[2].Kind != event.TurnEnd || !strings.Contains(got[1].Text, "redacted") {
		t.Fatalf("events: %+v", got)
	}
}

func TestBatchFallsBackForOlderDaemon(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte("101"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "batch.ready"), []byte("102"), 0600); err != nil {
		t.Fatal(err)
	}
	events := []event.Event{{Kind: event.ToolEdit, At: 1}, {Kind: event.TurnEnd, At: 2}}
	if err := SendBatch(dir, events); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "spool.jsonl"))
	if err != nil || strings.Count(string(b), "\n") != 2 || strings.Contains(string(b), `"events"`) {
		t.Fatalf("legacy format: %s %v", b, err)
	}
	var got []event.Event
	if err := ingestSpool(dir, func(ev event.Event) { got = append(got, ev) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnEnd {
		t.Fatalf("events: %+v", got)
	}
}

func TestConcurrentBatchesRemainContiguous(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	cleanup := markBatchReady(dir)
	defer cleanup()
	var group sync.WaitGroup
	for i := 0; i < 30; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			events := []event.Event{{Kind: event.ToolEdit, AgentSessionID: strconv.Itoa(i)}, {Kind: event.TurnEnd, AgentSessionID: strconv.Itoa(i)}}
			if err := SendBatch(dir, events); err != nil {
				t.Error(err)
			}
		}(i)
	}
	group.Wait()
	var got []event.Event
	if err := ingestSpool(dir, func(ev event.Event) { got = append(got, ev) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 60 {
		t.Fatalf("events=%d", len(got))
	}
	for i := 0; i < len(got); i += 2 {
		if got[i].Kind != event.ToolEdit || got[i+1].Kind != event.TurnEnd || got[i].AgentSessionID != got[i+1].AgentSessionID {
			t.Fatalf("interleaved events: %+v %+v", got[i], got[i+1])
		}
	}
}

func TestSocketBatchFollowsSpooledEvents(t *testing.T) {
	dir := t.TempDir()
	if err := appendSpool(dir, event.Event{Kind: event.TurnStart, At: 1}); err != nil {
		t.Fatal(err)
	}
	queue, seen := testDeliveryQueue(t, 3)
	delivery := orderedDelivery{dir: dir, queue: queue}
	if err := delivery.socketBatch([]event.Event{{Kind: event.ToolEdit, At: 2}, {Kind: event.TurnEnd, At: 3}}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []event.Kind{event.TurnStart, event.ToolEdit, event.TurnEnd} {
		if got := <-seen; got.Kind != expected {
			t.Fatalf("got %s, want %s", got.Kind, expected)
		}
	}
}

func TestContendedSpoolUsesNonblockingOverflow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	cleanup := markBatchReady(dir)
	defer cleanup()
	if err := appendSpool(dir, event.Event{Kind: event.TurnStart, At: 1}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "spool.jsonl"), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		}
	}()
	var durations []time.Duration
	for i := 0; i < 64; i++ {
		start := time.Now()
		events := []event.Event{{Kind: event.ToolEdit, At: int64(2*i + 2), Text: "ghp_abcdefghijklmnopqrstuvwxyz"}, {Kind: event.TurnEnd, At: int64(2*i + 3)}}
		if err := SendBatch(dir, events); err != nil {
			t.Fatal(err)
		}
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if durations[60] >= 20*time.Millisecond {
		t.Fatalf("contended spool p95=%s", durations[60])
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	locked = false
	// Later writes must not jump ahead of the overflow queue after unlock.
	if err := Send(dir, event.Event{Kind: event.SessionEnd, At: 130}); err != nil {
		t.Fatal(err)
	}
	var got []event.Event
	if err := ingestSpool(dir, func(ev event.Event) { got = append(got, ev) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 130 {
		t.Fatalf("events=%d", len(got))
	}
	for i, ev := range got {
		if ev.At != int64(i+1) || strings.Contains(ev.Text, "ghp_abcdefghijklmnopqrstuvwxyz") {
			t.Fatalf("event %d: %+v", i, ev)
		}
	}
	if err := ingestSpool(dir, func(event.Event) { t.Error("event replayed") }); err != nil {
		t.Fatal(err)
	}
}

func TestOverflowIgnoresIncompleteFrames(t *testing.T) {
	dir := t.TempDir()
	pending := filepath.Join(dir, "spool-pending")
	if err := os.MkdirAll(pending, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, "1.tmp"), []byte(`{"kind":"turn_start"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ingestSpool(dir, func(event.Event) { t.Error("incomplete frame was consumed") }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pending, "1.tmp")); err != nil {
		t.Fatalf("incomplete file changed: %v", err)
	}
}

func TestSpoolSkipsDamagedAndOversizedLines(t *testing.T) {
	dir := t.TempDir()
	content := `{"kind":"turn_start","at":1}` + "\n" + `{invalid` + "\n" + strings.Repeat("x", (4<<20)+1) + "\n" + `{"kind":"turn_end","at":2}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "spool.jsonl"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	var got []event.Event
	if err := ingestSpool(dir, func(ev event.Event) { got = append(got, ev) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].At != 1 || got[1].At != 2 {
		t.Fatalf("recovered events = %+v", got)
	}
}

func TestOverflowQuarantinesBadFramesAndContinues(t *testing.T) {
	dir := t.TempDir()
	pending := filepath.Join(dir, "spool-pending")
	if err := os.MkdirAll(pending, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"1.ready": "{invalid",
		"2.ready": strings.Repeat("x", (4<<20)+1),
		"3.ready": `{"kind":"turn_end","at":3}`,
	} {
		if err := os.WriteFile(filepath.Join(pending, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var got []event.Event
	if err := ingestSpool(dir, func(ev event.Event) { got = append(got, ev) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].At != 3 {
		t.Fatalf("recovered events = %+v", got)
	}
	for _, name := range []string{"1.ready.invalid", "2.ready.invalid"} {
		if _, err := os.Stat(filepath.Join(pending, name)); err != nil {
			t.Fatalf("bad frame not preserved: %s: %v", name, err)
		}
	}
	if err := ingestSpool(dir, func(event.Event) { t.Error("event replayed") }); err != nil {
		t.Fatal(err)
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
	seen := map[int64]bool{}
	if err := ingestSpool(dir, func(ev event.Event) {
		seen[ev.At] = true
	}); err != nil {
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

func TestSocketDaemonIngestsFallbackSpool(t *testing.T) {
	dir, err := os.MkdirTemp("", "tr-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ctx, cancel := context.WithCancel(context.Background())
	seen := make(chan event.Event, 2)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, dir, func(ev event.Event) { seen <- ev }) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("stopping daemon: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(filepath.Join(dir, "sock")); err == nil {
			break
		}
		if _, err := os.Stat(filepath.Join(dir, "spool.ready")); err == nil {
			t.Skip("Unix socket binding is unavailable in this sandbox")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "sock")); err != nil {
		t.Fatal("daemon socket did not start")
	}
	if err := appendSpool(dir, event.Event{Kind: event.TurnStart, Agent: "test", At: 42}); err != nil {
		t.Fatal(err)
	}
	if err := Send(dir, event.Event{Kind: event.TurnEnd, Agent: "test", At: 43}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{42, 43} {
		select {
		case ev := <-seen:
			if ev.At != want {
				t.Fatalf("delivery order: got %d, want %d", ev.At, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("socket daemon did not ingest the fallback and socket events")
		}
	}
}

func TestSocketDeliveryDrainsFallbackFirst(t *testing.T) {
	dir := t.TempDir()
	if err := appendSpool(dir, event.Event{Kind: event.TurnStart, Agent: "test", At: 1}); err != nil {
		t.Fatal(err)
	}
	queue, seen := testDeliveryQueue(t, 2)
	delivery := orderedDelivery{dir: dir, queue: queue}
	if err := delivery.socket(event.Event{Kind: event.TurnEnd, Agent: "test", At: 2}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{1, 2} {
		if got := (<-seen).At; got != want {
			t.Fatalf("delivery order: got %d, want %d", got, want)
		}
	}
}

func testDeliveryQueue(t *testing.T, capacity int) (chan queuedEvent, <-chan event.Event) {
	t.Helper()
	queue := make(chan queuedEvent, capacity)
	seen := make(chan event.Event, capacity)
	go func() {
		for item := range queue {
			seen <- item.event
			if item.done != nil {
				close(item.done)
			}
		}
	}()
	t.Cleanup(func() { close(queue) })
	return queue, seen
}

func TestSpoolRetainedWhenConsumptionInterrupted(t *testing.T) {
	dir := t.TempDir()
	for _, at := range []int64{1, 2} {
		if err := appendSpool(dir, event.Event{Kind: event.TurnStart, At: at}); err != nil {
			t.Fatal(err)
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected interruption")
			}
		}()
		_ = ingestSpool(dir, func(event.Event) { panic("interrupted") })
	}()
	var got []int64
	if err := ingestSpool(dir, func(ev event.Event) { got = append(got, ev.At) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("recovered events = %v", got)
	}
}

func TestSpoolWaitsForWorkerBeforeRemoval(t *testing.T) {
	dir := t.TempDir()
	if err := appendSpool(dir, event.Event{Kind: event.TurnStart, At: 1}); err != nil {
		t.Fatal(err)
	}
	queue := make(chan queuedEvent, 1)
	delivery := orderedDelivery{dir: dir, queue: queue}
	result := make(chan error, 1)
	go func() { result <- delivery.drain() }()
	item := <-queue
	b, err := os.ReadFile(filepath.Join(dir, "spool.jsonl"))
	if err != nil || len(b) == 0 {
		t.Fatalf("spool removed before worker completion: %q, %v", b, err)
	}
	item.done <- nil
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(dir, "spool.jsonl"))
	if err != nil || len(b) != 0 {
		t.Fatalf("spool not cleared after worker completion: %q, %v", b, err)
	}
}

func TestDeliveryRetainsFrameAfterWorkerError(t *testing.T) {
	dir := t.TempDir()
	if err := appendSpool(dir, event.Event{Kind: event.TurnStart, At: 1}); err != nil {
		t.Fatal(err)
	}
	queue := make(chan queuedEvent, 1)
	delivery := orderedDelivery{dir: dir, queue: queue}
	result := make(chan error, 1)
	go func() { result <- delivery.drain() }()
	item := <-queue
	item.done <- errors.New("worker failed")
	if err := <-result; err == nil {
		t.Fatal("worker error was ignored")
	}
	var replayed []event.Event
	if err := ingestSpool(dir, func(ev event.Event) { replayed = append(replayed, ev) }); err != nil || len(replayed) != 1 {
		t.Fatalf("frame was not retained: %+v, %v", replayed, err)
	}
}

func TestSocketBatchIsSpooledAfterBacklogFailure(t *testing.T) {
	dir := t.TempDir()
	if err := appendSpool(dir, event.Event{Kind: event.TurnStart, At: 1}); err != nil {
		t.Fatal(err)
	}
	queue := make(chan queuedEvent, 1)
	delivery := orderedDelivery{dir: dir, queue: queue}
	result := make(chan error, 1)
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	go func() {
		result <- delivery.socketBatch([]event.Event{{Kind: event.TurnContext, At: 2, Text: secret}, {Kind: event.TurnEnd, At: 3}})
	}()
	item := <-queue
	item.done <- errors.New("worker failed")
	if err := <-result; err == nil {
		t.Fatal("backlog failure was ignored")
	}
	data, err := os.ReadFile(filepath.Join(dir, "spool.jsonl"))
	if err != nil || strings.Contains(string(data), secret) {
		t.Fatalf("unsafe spool: %s, %v", data, err)
	}
	var got []event.Event
	if err := ingestSpool(dir, func(ev event.Event) { got = append(got, ev) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Kind != event.TurnStart || got[1].Kind != event.TurnContext || got[2].Kind != event.TurnEnd {
		t.Fatalf("lost or reordered events: %+v", got)
	}
}

func TestSocketBatchRetainsFailedSuffix(t *testing.T) {
	dir := t.TempDir()
	queue := make(chan queuedEvent, 1)
	delivery := orderedDelivery{dir: dir, queue: queue}
	result := make(chan error, 1)
	go func() {
		result <- delivery.socketBatch([]event.Event{{Kind: event.TurnStart, At: 1}, {Kind: event.TurnContext, At: 2}, {Kind: event.TurnEnd, At: 3}})
	}()
	first := <-queue
	first.done <- nil
	second := <-queue
	second.done <- errors.New("worker failed")
	if err := <-result; err == nil {
		t.Fatal("worker failure was ignored")
	}
	var got []event.Event
	if err := ingestSpool(dir, func(ev event.Event) { got = append(got, ev) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != event.TurnContext || got[1].Kind != event.TurnEnd {
		t.Fatalf("incorrect retry suffix: %+v", got)
	}
}

func TestLongSocketPathFallsBackToSpool(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("x", 120))
	ctx, cancel := context.WithCancel(context.Background())
	seen := make(chan event.Event, 1)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, dir, func(ev event.Event) { seen <- ev }) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("stopping spool daemon: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("spool daemon did not stop")
		}
	})
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(filepath.Join(dir, "spool.ready")); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "spool.ready")); err != nil {
		t.Fatal("long socket path did not switch to spool mode")
	}
	if err := Send(dir, event.Event{Kind: event.TurnStart, Agent: "test", At: 73}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-seen:
		if ev.At != 73 {
			t.Fatalf("unexpected spooled event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("spool daemon did not ingest event from a long socket path")
	}
}
