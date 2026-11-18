package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/id"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

func Serve(ctx context.Context, dir string, consume func(event.Event)) error {
	return ServeChecked(ctx, dir, func(ev event.Event) error { consume(ev); return nil })
}

// ServeChecked retains spooled frames when the processing callback fails.
func ServeChecked(ctx context.Context, dir string, consume func(event.Event) error) error {
	return ServeCheckedWithLogger(ctx, dir, slog.Default(), consume)
}

// ServeCheckedWithLogger retains spooled frames when the processing callback
// fails and reports undecodable frames through the supplied logger.
func ServeCheckedWithLogger(ctx context.Context, dir string, logger *slog.Logger, consume func(event.Event) error) error {
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating daemon directory: %w", err)
	}
	sock := filepath.Join(dir, "sock")
	if conn, err := net.DialTimeout("unix", sock, 20*time.Millisecond); err == nil {
		conn.Close()
		return fmt.Errorf("daemon already listens on %s", sock)
	}
	if err := os.Remove(sock); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing stale socket: %w", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENAMETOOLONG) {
			cleanup := markBatchReady(dir)
			defer cleanup()
			return serveSpoolChecked(ctx, dir, consume)
		}
		return fmt.Errorf("listening on socket: %w", err)
	}
	defer ln.Close()
	defer os.Remove(sock)
	cleanup := markBatchReady(dir)
	defer cleanup()
	if err := os.Chmod(sock, 0600); err != nil {
		return fmt.Errorf("setting socket mode: %w", err)
	}
	queue := make(chan queuedEvent, 1024)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for item := range queue {
			err := consume(item.event)
			if item.done != nil {
				item.done <- err
			}
		}
	}()
	delivery := orderedDelivery{dir: dir, queue: queue}
	if err := delivery.drain(); err != nil {
		close(queue)
		workers.Wait()
		return err
	}
	pollCtx, stopPoll := context.WithCancel(ctx)
	pollDone := make(chan error, 1)
	go func() {
		err := pollSpool(pollCtx, delivery.drain)
		if err != nil {
			_ = ln.Close()
		}
		pollDone <- err
	}()
	go func() { <-pollCtx.Done(); _ = ln.Close() }()
	var acceptErr error
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil {
				acceptErr = fmt.Errorf("accepting event: %w", err)
			}
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		s := bufio.NewScanner(conn)
		s.Buffer(make([]byte, 4096), 4<<20)
		for s.Scan() {
			raw := s.Bytes()
			events, err := decodeFrame(raw)
			if err != nil {
				// Never silently discard a frame the sender believed it sent.
				// Keep a redacted copy for inspection and log a redacted hint.
				if qerr := quarantineFrame(dir, raw); qerr != nil {
					logger.Error("quarantining invalid socket frame", "error", qerr)
				}
				logger.Warn("discarding invalid socket frame", "reason", err, "preview", redactPreview(raw))
				continue
			}
			if err := delivery.socketBatch(events); err != nil {
				acceptErr = fmt.Errorf("delivering socket event: %w", err)
				break
			}
		}
		if err := s.Err(); err != nil {
			logger.Warn("reading socket frames stopped", "error", err)
		}
		conn.Close()
		if acceptErr != nil {
			break
		}
	}
	stopPoll()
	pollErr := <-pollDone
	close(queue)
	workers.Wait()
	if pollErr != nil {
		return fmt.Errorf("ingesting spooled events: %w", pollErr)
	}
	return acceptErr
}

func markBatchReady(dir string) func() {
	path := filepath.Join(dir, "batch.ready")
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		return func() {}
	}
	durable := filepath.Join(dir, "durable.ready")
	_ = os.WriteFile(durable, []byte(strconv.Itoa(os.Getpid())), 0600)
	return func() { _ = os.Remove(path); _ = os.Remove(durable) }
}

// orderedDelivery keeps a completed fallback write ahead of later socket events.
type orderedDelivery struct {
	mu    sync.Mutex
	dir   string
	queue chan<- queuedEvent
}

type queuedEvent struct {
	event event.Event
	done  chan error
}

func (d *orderedDelivery) deliverSpool(ev event.Event) error {
	done := make(chan error, 1)
	d.queue <- queuedEvent{event: ev, done: done}
	return <-done
}

func (d *orderedDelivery) drain() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ingestSpoolChecked(d.dir, d.deliverSpool)
}

func (d *orderedDelivery) socket(ev event.Event) error {
	return d.socketBatch([]event.Event{ev})
}

func (d *orderedDelivery) socketBatch(events []event.Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ingestSpoolChecked(d.dir, d.deliverSpool); err != nil {
		// The sender has already written this frame and cannot observe the
		// processing failure. Preserve it after the failed backlog before
		// stopping the daemon, rather than silently dropping it.
		return d.preserveSocketEvents(events, err)
	}
	for i, ev := range events {
		if err := d.deliverSpool(ev.Sanitized()); err != nil {
			return d.preserveSocketEvents(events[i:], err)
		}
	}
	return nil
}

func (d *orderedDelivery) preserveSocketEvents(events []event.Event, cause error) error {
	clean := make([]event.Event, len(events))
	for i := range events {
		clean[i] = events[i].Sanitized()
	}
	b, err := json.Marshal(struct {
		Events []event.Event `json:"events"`
	}{clean})
	if err != nil {
		return fmt.Errorf("encoding socket events after processing failure: %w", err)
	}
	if err := appendSpoolFrame(d.dir, append(b, '\n')); err != nil {
		return fmt.Errorf("preserving socket events after processing failure %v: %w", cause, err)
	}
	return cause
}

// serveSpool keeps local capture usable where Unix socket binding is denied.
// Hooks still use their bounded socket attempt and append-only spool fallback.
func serveSpool(ctx context.Context, dir string, consume func(event.Event)) error {
	return serveSpoolChecked(ctx, dir, func(ev event.Event) error { consume(ev); return nil })
}

func serveSpoolChecked(ctx context.Context, dir string, consume func(event.Event) error) error {
	ready := filepath.Join(dir, "spool.ready")
	if err := os.WriteFile(ready, []byte("1\n"), 0600); err != nil {
		return fmt.Errorf("marking spool daemon ready: %w", err)
	}
	defer os.Remove(ready)
	if err := ingestSpoolChecked(dir, consume); err != nil {
		return err
	}
	return pollSpool(ctx, func() error { return ingestSpoolChecked(dir, consume) })
}

func pollSpool(ctx context.Context, drain func() error) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := drain(); err != nil {
				return err
			}
		}
	}
}

// quarantineFrame preserves an undecodable frame for local inspection. The
// bytes are redacted first so quarantining never becomes a way to persist a
// secret that hook-side redaction failed to remove.
func quarantineFrame(dir string, raw []byte) error {
	qdir := filepath.Join(dir, "quarantine")
	if err := os.MkdirAll(qdir, 0700); err != nil {
		return fmt.Errorf("creating quarantine directory: %w", err)
	}
	name, err := id.New()
	if err != nil {
		return fmt.Errorf("naming quarantined frame: %w", err)
	}
	data := (redact.Redactor{}).Text(string(raw))
	if err := os.WriteFile(filepath.Join(qdir, name+".frame"), []byte(data), 0600); err != nil {
		return fmt.Errorf("writing quarantined frame: %w", err)
	}
	return nil
}

func redactPreview(raw []byte) string {
	return textutil.Truncate((redact.Redactor{}).Text(string(raw)), 160)
}
