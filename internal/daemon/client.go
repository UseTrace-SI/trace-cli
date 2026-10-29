package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func Send(dir string, ev event.Event) error {
	ev = ev.Sanitized()
	b, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("encoding event: %w", err)
	}
	return sendFrame(dir, append(b, '\n'))
}

// SendBatch sends one hook callback as one JSON line, preserving event order.
func SendBatch(dir string, events []event.Event) error {
	if len(events) == 0 {
		return nil
	}
	if len(events) == 1 {
		return Send(dir, events[0])
	}
	// An older daemon understands only individual events. Its pid will not
	// match the marker written by a batch-capable daemon.
	if !batchReady(dir) {
		var first error
		for _, ev := range events {
			if err := Send(dir, ev); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	clean := make([]event.Event, len(events))
	for i := range events {
		clean[i] = events[i].Sanitized()
	}
	b, err := json.Marshal(struct {
		Events []event.Event `json:"events"`
	}{clean})
	if err != nil {
		return fmt.Errorf("encoding event batch: %w", err)
	}
	return sendFrame(dir, append(b, '\n'))
}

func batchReady(dir string) bool {
	marker, err := os.ReadFile(filepath.Join(dir, "batch.ready"))
	if err != nil || len(marker) == 0 {
		return false
	}
	pid, err := os.ReadFile(filepath.Join(dir, "daemon.pid"))
	return err == nil && string(pid) == string(marker)
}

func sendFrame(dir string, b []byte) error {
	conn, err := net.DialTimeout("unix", filepath.Join(dir, "sock"), 5*time.Millisecond)
	if err == nil {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Millisecond))
		_, err = conn.Write(b)
		_ = conn.Close()
		if err == nil {
			return nil
		}
	}
	return appendSpoolFrame(dir, b)
}
