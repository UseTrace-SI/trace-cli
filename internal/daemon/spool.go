package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func appendSpool(dir string, ev event.Event) error {
	b, err := json.Marshal(ev.Sanitized())
	if err != nil {
		return fmt.Errorf("encoding event: %w", err)
	}
	return appendSpoolFrame(dir, append(b, '\n'))
}

func appendSpoolFrame(dir string, b []byte) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating spool directory: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "spool-overflow")); err == nil {
		return appendOverflow(dir, b)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking spool overflow mode: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "spool.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
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
	if _, err := os.Stat(filepath.Join(dir, "spool-overflow")); err == nil {
		return appendOverflow(dir, b)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking spool overflow mode: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("writing spool: %w", err)
	}
	return nil
}

// appendOverflow publishes a complete, redacted frame by atomic rename. It
// never waits for the shared spool lock; partial files are never consumed.
func appendOverflow(dir string, b []byte) error {
	marker, err := os.OpenFile(filepath.Join(dir, "spool-overflow"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("enabling spool overflow: %w", err)
	}
	if err := marker.Close(); err != nil {
		return fmt.Errorf("closing spool overflow marker: %w", err)
	}
	pending := filepath.Join(dir, "spool-pending")
	if err := os.MkdirAll(pending, 0700); err != nil {
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
	f, err := os.OpenFile(filepath.Join(dir, "spool.jsonl"), os.O_CREATE|os.O_RDWR, 0600)
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
	if err := readSpoolFrames(f, consume); err != nil {
		return fmt.Errorf("reading spool: %w", err)
	}
	// Keep the original frames until their callbacks finish. If processing is
	// interrupted, replay is preferable to silently losing the queued events.
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncating spool: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unlocking spool: %w", err)
	}
	locked = false
	return ingestOverflow(dir, consume)
}

// readSpoolFrames bounds memory per line and skips damaged or oversized lines,
// allowing later valid events to be recovered instead of stopping the daemon.
func readSpoolFrames(r io.Reader, consume func(event.Event) error) error {
	const maxFrame = 4 << 20
	reader := bufio.NewReaderSize(r, 4096)
	var line []byte
	oversized := false
	for {
		part, err := reader.ReadSlice('\n')
		if !oversized {
			if len(line)+len(part) > maxFrame {
				oversized = true
				line = nil
			} else {
				line = append(line, part...)
			}
		}
		if err == nil || err == io.EOF {
			if !oversized && len(line) != 0 {
				if events, decodeErr := decodeFrame(line); decodeErr == nil {
					for _, ev := range events {
						if err := consume(ev); err != nil {
							return fmt.Errorf("processing spooled event: %w", err)
						}
					}
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

func ingestOverflow(dir string, consume func(event.Event) error) error {
	pending := filepath.Join(dir, "spool-pending")
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
		for _, ev := range events {
			if err := consume(ev); err != nil {
				return fmt.Errorf("processing pending spool event: %w", err)
			}
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing pending spool frame: %w", err)
		}
	}
	return nil
}
