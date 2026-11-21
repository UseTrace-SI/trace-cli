package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	if err := ensureStateDir(dir); err != nil {
		return fmt.Errorf("creating daemon directory: %w", err)
	}
	sock := filepath.Join(dir, "sock")
	if conn, err := dialDaemon(dir, 20*time.Millisecond); err == nil {
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
			return serveSpoolChecked(ctx, dir, logger, consume)
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
	delivery := orderedDelivery{dir: dir, queue: queue, retry: frameRetry{logger: logger}}
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
	if err := WriteStateFile(path, []byte(strconv.Itoa(os.Getpid()))); err != nil {
		return func() {}
	}
	durable := filepath.Join(dir, "durable.ready")
	_ = WriteStateFile(durable, []byte(strconv.Itoa(os.Getpid())))
	return func() { _ = os.Remove(path); _ = os.Remove(durable) }
}

const (
	// A frame whose processing keeps failing is retried after doubling
	// delays, then quarantined, so one event cannot block every later one.
	maxFrameAttempts = 3
	frameRetryDelay  = 250 * time.Millisecond
)

// frameRetry paces retries of the spooled frame whose processing failed.
// Processing failures never stop the daemon.
type frameRetry struct {
	logger   *slog.Logger
	key      [sha256.Size]byte
	attempts int
	next     time.Time
}

// drain delivers the spool unless a failed frame still awaits its retry. A
// processing failure is logged rather than returned; pending then reports
// that a backlog remains ahead of newer events.
func (r *frameRetry) drain(dir string, consume func(event.Event) error) (pending bool, err error) {
	if time.Now().Before(r.next) {
		return true, nil
	}
	err = drainSpool(dir, consume, r.failed)
	var failed *processingError
	if errors.As(err, &failed) {
		return true, nil
	}
	if err == nil {
		r.attempts = 0
	}
	return false, err
}

// failed records a failed attempt on frame, which stays first in the queue,
// and reports whether to quarantine it instead of retrying.
func (r *frameRetry) failed(frame []byte, err error) bool {
	logger := r.logger
	if logger == nil {
		logger = slog.Default()
	}
	if key := sha256.Sum256(frame); key != r.key {
		r.key, r.attempts = key, 0
	}
	r.attempts++
	reason := (redact.Redactor{}).Text(err.Error())
	if r.attempts >= maxFrameAttempts {
		logger.Error("quarantining spooled frame after repeated processing failures", "attempts", r.attempts, "error", reason)
		r.attempts = 0
		return true
	}
	delay := frameRetryDelay << (r.attempts - 1)
	r.next = time.Now().Add(delay)
	logger.Warn("processing spooled frame failed; retrying", "attempt", r.attempts, "delay", delay, "error", reason)
	return false
}

// orderedDelivery keeps a completed fallback write ahead of later socket events.
type orderedDelivery struct {
	mu    sync.Mutex
	dir   string
	queue chan<- queuedEvent
	retry frameRetry
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
	_, err := d.retry.drain(d.dir, d.deliverSpool)
	return err
}

func (d *orderedDelivery) socket(ev event.Event) error {
	return d.socketBatch([]event.Event{ev})
}

func (d *orderedDelivery) socketBatch(events []event.Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if pending, err := d.retry.drain(d.dir, d.deliverSpool); err != nil || pending {
		// The sender has already written this frame and cannot observe the
		// failed or waiting backlog. Keep the frame behind it rather than
		// dropping it or letting it overtake the backlog.
		if preserveErr := d.preserveSocketEvents(events); preserveErr != nil {
			return errors.Join(err, preserveErr)
		}
		return err
	}
	for i, ev := range events {
		if err := d.deliverSpool(ev.Sanitized()); err != nil {
			// Retry the rest from the spool like any failed spooled frame.
			return d.preserveSocketEvents(events[i:])
		}
	}
	return nil
}

func (d *orderedDelivery) preserveSocketEvents(events []event.Event) error {
	b, err := encodeBatch(events)
	if err != nil {
		return fmt.Errorf("encoding socket events after processing failure: %w", err)
	}
	if err := appendSpoolFrame(d.dir, b); err != nil {
		return fmt.Errorf("preserving socket events after processing failure: %w", err)
	}
	return nil
}

// serveSpool keeps local capture usable where Unix socket binding is denied.
// Hooks still use their bounded socket attempt and append-only spool fallback.
func serveSpool(ctx context.Context, dir string, consume func(event.Event)) error {
	return serveSpoolChecked(ctx, dir, slog.Default(), func(ev event.Event) error { consume(ev); return nil })
}

func serveSpoolChecked(ctx context.Context, dir string, logger *slog.Logger, consume func(event.Event) error) error {
	ready := filepath.Join(dir, "spool.ready")
	if err := WriteStateFile(ready, []byte("1\n")); err != nil {
		return fmt.Errorf("marking spool daemon ready: %w", err)
	}
	defer os.Remove(ready)
	retry := frameRetry{logger: logger}
	drain := func() error { _, err := retry.drain(dir, consume); return err }
	if err := drain(); err != nil {
		return err
	}
	return pollSpool(ctx, drain)
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
	// Pruning deletes files, so never use a symlinked quarantine directory.
	if err := ensureStateDir(qdir); err != nil {
		return fmt.Errorf("creating quarantine directory: %w", err)
	}
	name, err := id.New()
	if err != nil {
		return fmt.Errorf("naming quarantined frame: %w", err)
	}
	data := (redact.Redactor{}).Text(string(raw))
	if err := WriteStateFile(filepath.Join(qdir, name+".frame"), []byte(data)); err != nil {
		return fmt.Errorf("writing quarantined frame: %w", err)
	}
	// Socket quarantine needs the same bounds as spool quarantine: a stream of
	// malformed frames must not fill the disk (which would also break spooling).
	if err := pruneQuarantine(dir); err != nil {
		return fmt.Errorf("pruning quarantined frames: %w", err)
	}
	return nil
}

// pruneQuarantine caps both the age and the number of quarantined socket
// frames. Fresh frames are kept newest-first so recent evidence survives.
func pruneQuarantine(dir string) error {
	qdir := filepath.Join(dir, "quarantine")
	entries, err := os.ReadDir(qdir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing quarantined frames: %w", err)
	}
	type frame struct {
		path string
		when time.Time
	}
	var fresh []frame
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".frame") {
			continue
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspecting quarantined frame: %w", err)
		}
		path := filepath.Join(qdir, entry.Name())
		if now.Sub(info.ModTime()) > spoolRetention {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing old quarantined frame: %w", err)
			}
			continue
		}
		fresh = append(fresh, frame{path: path, when: info.ModTime()})
	}
	if len(fresh) > maxQuarantinedFrames {
		sort.Slice(fresh, func(i, j int) bool { return fresh[i].when.Before(fresh[j].when) })
		for _, old := range fresh[:len(fresh)-maxQuarantinedFrames] {
			if err := os.Remove(old.path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing excess quarantined frame: %w", err)
			}
		}
	}
	return nil
}

// quarantineFailedFrame keeps a frame that repeatedly failed to process,
// with its error, as a batch line that can be inspected or replayed.
func quarantineFailedFrame(dir string, frame []byte, cause error) error {
	events, err := decodeFrame(frame)
	if err != nil {
		return quarantineFrame(dir, frame)
	}
	record, err := json.Marshal(struct {
		Error  string        `json:"error"`
		Events []event.Event `json:"events"`
	}{cause.Error(), events})
	if err != nil {
		return fmt.Errorf("encoding quarantined frame: %w", err)
	}
	return quarantineFrame(dir, append(record, '\n'))
}

func redactPreview(raw []byte) string {
	return textutil.Truncate((redact.Redactor{}).Text(string(raw)), 160)
}
