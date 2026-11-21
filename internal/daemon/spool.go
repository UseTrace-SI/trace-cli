package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

const (
	spoolOverflowMarker = "spool-overflow"
	spoolOverflowLock   = "spool-overflow.lock"
	// Older quarantined/incomplete frames are dropped after this long, and at
	// most this many quarantined frames are kept, so a long-lived overflow mode
	// cannot accumulate files without bound.
	spoolRetention       = 24 * time.Hour
	maxQuarantinedFrames = 32
)

func appendSpool(dir string, ev event.Event) error {
	b, err := json.Marshal(ev.Sanitized())
	if err != nil {
		return fmt.Errorf("encoding event: %w", err)
	}
	return appendSpoolFrame(dir, append(b, '\n'))
}

func appendSpoolFrame(dir string, b []byte) error {
	if err := ensureStateDir(dir); err != nil {
		return fmt.Errorf("creating spool directory: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dir, spoolOverflowMarker)); err == nil {
		return appendOverflow(dir, b)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking spool overflow mode: %w", err)
	}
	f, err := openStateFile(filepath.Join(dir, "spool.jsonl"), os.O_CREATE|os.O_RDWR|os.O_APPEND)
	if err != nil {
		return fmt.Errorf("opening spool: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return appendOverflow(dir, b)
		}
		return fmt.Errorf("locking spool: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	// Once overflow is active, later fallback writes use the same queue so
	// they cannot overtake already completed overflow writes.
	if _, err := os.Stat(filepath.Join(dir, spoolOverflowMarker)); err == nil {
		return appendOverflow(dir, b)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking spool overflow mode: %w", err)
	}
	// An earlier append may have been cut short (a full disk, or a hook
	// killed mid-write). Start a new line so its fragment cannot absorb this
	// frame, and roll back a failed write so it leaves no fragment itself.
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("finding spool end: %w", err)
	}
	if end > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, end-1); err != nil {
			return fmt.Errorf("reading spool end: %w", err)
		}
		if last[0] != '\n' {
			b = append([]byte{'\n'}, b...)
		}
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Truncate(end)
		return fmt.Errorf("writing spool: %w", err)
	}
	return nil
}

// overflowLock guards the transition into and out of overflow mode. The
// daemon holds it only while clearing a drained marker; hooks take it briefly
// when they switch to overflow. This keeps a concurrent hook from switching
// back to overflow between the daemon's final drain and marker removal.
func overflowLock(dir string) (*os.File, error) {
	f, err := openStateFile(filepath.Join(dir, spoolOverflowLock), os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, fmt.Errorf("opening spool overflow lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("locking spool overflow: %w", err)
	}
	return f, nil
}

// appendOverflow publishes a complete, redacted frame by atomic rename. It
// never waits for the shared spool lock; partial files are never consumed.
func appendOverflow(dir string, b []byte) error {
	lock, err := overflowLock(dir)
	if err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}()
	marker, err := openStateFile(filepath.Join(dir, spoolOverflowMarker), os.O_CREATE|os.O_WRONLY)
	if err != nil {
		return fmt.Errorf("enabling spool overflow: %w", err)
	}
	if err := marker.Close(); err != nil {
		return fmt.Errorf("closing spool overflow marker: %w", err)
	}
	pending := filepath.Join(dir, "spool-pending")
	if err := ensureStateDir(pending); err != nil {
		return fmt.Errorf("creating pending spool: %w", err)
	}
	f, err := os.CreateTemp(pending, fmt.Sprintf("%020d-*.tmp", time.Now().UnixNano()))
	if err != nil {
		return fmt.Errorf("creating pending spool frame: %w", err)
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(b); err != nil {
		f.Close()
		return fmt.Errorf("writing pending spool frame: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing pending spool frame: %w", err)
	}
	if err := os.Rename(name, strings.TrimSuffix(name, ".tmp")+".ready"); err != nil {
		return fmt.Errorf("publishing pending spool frame: %w", err)
	}
	return nil
}

func ingestSpool(dir string, consume func(event.Event)) error {
	return ingestSpoolChecked(dir, func(ev event.Event) error { consume(ev); return nil })
}

func ingestSpoolChecked(dir string, consume func(event.Event) error) error {
	return drainSpool(dir, consume, nil)
}

// processingError reports that the processing callback failed. The failed
// event and everything after it were kept for a later attempt.
type processingError struct{ err error }

func (e *processingError) Error() string { return e.err.Error() }
func (e *processingError) Unwrap() error { return e.err }

// drainSpool delivers the spool and then the overflow queue, in order. After
// a processing failure only the failed event and the ones after it are kept,
// so a retry never repeats processed events; a crash can still replay them,
// which keeps delivery at least once. giveUp, when set, learns of each
// failure and may quarantine the frame so that later frames can continue.
func drainSpool(dir string, consume func(event.Event) error, giveUp func(frame []byte, err error) bool) error {
	f, err := openStateFile(filepath.Join(dir, "spool.jsonl"), os.O_CREATE|os.O_RDWR)
	if err != nil {
		return fmt.Errorf("opening spool: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking spool: %w", err)
	}
	locked := true
	defer func() {
		if locked {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		}
	}()
	var head []byte
	keepFrom := int64(-1)
	err = readSpoolFrames(f, func(b []byte) error { return quarantineFrame(dir, b) }, func(line []byte, events []event.Event, end int64) error {
		frame, err := deliverFrame(line, events, consume)
		if err == nil {
			return nil
		}
		if giveUp != nil && giveUp(frame, err) {
			return quarantineFailedFrame(dir, frame, err)
		}
		head, keepFrom = frame, end
		return &processingError{err}
	})
	if keepFrom >= 0 {
		// The frames before the failed one were processed: drop them.
		if keepErr := keepSpoolSuffix(f, head, keepFrom); keepErr != nil {
			return fmt.Errorf("keeping unprocessed spool frames: %w", keepErr)
		}
		return fmt.Errorf("processing spooled event: %w", err)
	}
	if err != nil {
		return fmt.Errorf("reading spool: %w", err)
	}
	// Keep the original frames until their callbacks finish. If processing is
	// interrupted, replay is preferable to silently losing the queued events.
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncating spool: %w", err)
	}
	// Drain the overflow backlog while still holding the spool lock, so no
	// writer can fall back to the main spool before the queue is empty (which
	// would let a later write overtake an earlier overflow frame).
	if err := ingestOverflow(dir, consume, giveUp); err != nil {
		return err
	}
	if err := clearOverflowMarker(dir); err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unlocking spool: %w", err)
	}
	locked = false
	return nil
}

// deliverFrame consumes a frame's events in order. On failure it returns
// what is left to retry: the line itself if no event was consumed, otherwise
// the remaining events re-encoded as one line.
func deliverFrame(line []byte, events []event.Event, consume func(event.Event) error) ([]byte, error) {
	for i, ev := range events {
		if err := consume(ev); err != nil {
			if i > 0 {
				if rest, encodeErr := encodeBatch(events[i:]); encodeErr == nil {
					return rest, err
				}
				// Replaying the processed events beats losing the rest.
			}
			return line, err
		}
	}
	return nil, nil
}

// encodeBatch encodes events as one redacted spool line.
func encodeBatch(events []event.Event) ([]byte, error) {
	clean := make([]event.Event, len(events))
	for i := range events {
		clean[i] = events[i].Sanitized()
	}
	b, err := json.Marshal(struct {
		Events []event.Event `json:"events"`
	}{clean})
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// keepSpoolSuffix rewrites the locked spool as head followed by its bytes
// from offset on. Interrupted part-way, it can repeat retained frames but
// cannot lose them.
func keepSpoolSuffix(f *os.File, head []byte, offset int64) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	rest, err := io.ReadAll(io.NewSectionReader(f, offset, info.Size()-offset))
	if err != nil {
		return err
	}
	data := append(append(make([]byte, 0, len(head)+len(rest)), head...), rest...)
	if _, err := f.WriteAt(data, 0); err != nil {
		return err
	}
	return f.Truncate(int64(len(data)))
}

// readSpoolFrames bounds memory per line and quarantines damaged or oversized
// lines before skipping them, allowing later valid events to be recovered
// instead of silently discarding a frame the sender believed it sent. deliver
// receives each decoded line with the offset just past it.
func readSpoolFrames(r io.Reader, quarantine func([]byte) error, deliver func(line []byte, events []event.Event, end int64) error) error {
	const maxFrame = 4 << 20
	reader := bufio.NewReaderSize(r, 4096)
	var line []byte
	var offset int64
	oversized := false
	for {
		part, err := reader.ReadSlice('\n')
		offset += int64(len(part))
		if !oversized {
			if len(line)+len(part) > maxFrame {
				oversized = true
				// Keep the bounded prefix accumulated so far as evidence.
			} else {
				line = append(line, part...)
			}
		}
		if err == nil || err == io.EOF {
			if oversized {
				if qerr := quarantine(line); qerr != nil {
					return fmt.Errorf("quarantining oversized spool frame: %w", qerr)
				}
			} else if len(line) != 0 {
				if events, decodeErr := decodeFrame(line); decodeErr == nil {
					if err := deliver(line, events, offset); err != nil {
						return err
					}
				} else if qerr := quarantine(line); qerr != nil {
					return fmt.Errorf("quarantining damaged spool frame: %w", qerr)
				}
			}
			line = nil
			oversized = false
		}
		if err == io.EOF {
			return nil
		}
		if err != nil && err != bufio.ErrBufferFull {
			return err
		}
	}
}

// clearOverflowMarker returns the spool to normal append mode once the backlog
// is drained. It runs while the daemon holds the spool lock, and takes the
// overflow lock so a hook cannot re-enter overflow mode during the transition.
func clearOverflowMarker(dir string) error {
	marker := filepath.Join(dir, spoolOverflowMarker)
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		return nil
	}
	lock, err := overflowLock(dir)
	if err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}()
	// Recheck under the lock; another writer may have cleared it already.
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		return nil
	}
	pending := filepath.Join(dir, "spool-pending")
	entries, err := os.ReadDir(pending)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("listing pending spool: %w", err)
	}
	for _, entry := range entries {
		// A ready frame has not been consumed yet; a tmp frame is still being
		// written. Keep overflow mode until neither is present.
		if strings.HasSuffix(entry.Name(), ".ready") || strings.HasSuffix(entry.Name(), ".tmp") {
			return nil
		}
	}
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clearing spool overflow marker: %w", err)
	}
	return nil
}

// pruneOverflowFrames bounds the number and age of quarantined and incomplete
// frames under spool-pending, while leaving freshly written tmp files alone.
func pruneOverflowFrames(dir string) error {
	pending := filepath.Join(dir, "spool-pending")
	entries, err := os.ReadDir(pending)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing pending spool: %w", err)
	}
	type quarantined struct {
		path string
		when time.Time
	}
	var invalid []quarantined
	now := time.Now()
	for _, entry := range entries {
		name := entry.Name()
		isInvalid := strings.HasSuffix(name, ".invalid")
		if !isInvalid && !strings.HasSuffix(name, ".tmp") {
			continue
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspecting pending spool frame: %w", err)
		}
		path := filepath.Join(pending, name)
		if now.Sub(info.ModTime()) > spoolRetention {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("pruning pending spool frame: %w", err)
			}
			continue
		}
		if isInvalid {
			invalid = append(invalid, quarantined{path: path, when: info.ModTime()})
		}
	}
	if len(invalid) > maxQuarantinedFrames {
		sort.Slice(invalid, func(i, j int) bool { return invalid[i].when.Before(invalid[j].when) })
		for _, old := range invalid[:len(invalid)-maxQuarantinedFrames] {
			if err := os.Remove(old.path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("pruning quarantined spool frame: %w", err)
			}
		}
	}
	return nil
}

func ingestOverflow(dir string, consume func(event.Event) error, giveUp func(frame []byte, err error) bool) error {
	pending := filepath.Join(dir, "spool-pending")
	// Consuming and pruning delete files, so never list a symlinked queue.
	if err := checkStateDir(pending); err != nil {
		return fmt.Errorf("checking pending spool: %w", err)
	}
	if err := pruneOverflowFrames(dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(pending)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing pending spool: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".ready") || !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(pending, entry.Name())
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading pending spool frame: %w", err)
		}
		b, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
		closeErr := f.Close()
		if err != nil {
			return fmt.Errorf("reading pending spool frame: %w", err)
		}
		if closeErr != nil {
			return fmt.Errorf("closing pending spool frame: %w", closeErr)
		}
		var events []event.Event
		var decodeErr error
		if len(b) <= 4<<20 {
			events, decodeErr = decodeFrame(b)
		}
		if len(b) > 4<<20 || decodeErr != nil {
			// Preserve the original file for inspection, but keep it out of the
			// ready queue so it cannot prevent later frames from being read.
			if err := os.Rename(path, path+".invalid"); err != nil {
				return fmt.Errorf("quarantining pending spool frame: %w", err)
			}
			continue
		}
		if frame, err := deliverFrame(b, events, consume); err != nil {
			if giveUp == nil || !giveUp(frame, err) {
				// Keep only the events of this frame that were not processed.
				if !bytes.Equal(frame, b) {
					if err := WriteStateFile(path, frame); err != nil {
						return fmt.Errorf("keeping unprocessed pending spool events: %w", err)
					}
				}
				return fmt.Errorf("processing pending spool event: %w", &processingError{err})
			}
			if err := quarantineFailedFrame(dir, frame, err); err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing pending spool frame: %w", err)
		}
	}
	return nil
}
