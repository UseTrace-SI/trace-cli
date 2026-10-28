package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func appendSpool(dir string, ev event.Event) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating spool directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "spool.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("opening spool: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking spool: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	b, err := json.Marshal(ev.Sanitized())
	if err != nil {
		return fmt.Errorf("encoding event: %w", err)
	}
	b = append(b, '\n')
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("writing spool: %w", err)
	}
	return nil
}

func ingestSpool(dir string, consume func(event.Event)) error {
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
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 4<<20)
	var events []event.Event
	for s.Scan() {
		var ev event.Event
		if err := json.Unmarshal(s.Bytes(), &ev); err == nil {
			events = append(events, ev)
		}
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("reading spool: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncating spool: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unlocking spool: %w", err)
	}
	locked = false
	for _, ev := range events {
		consume(ev)
	}
	return nil
}
