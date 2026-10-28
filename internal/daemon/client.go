package daemon

import (
	"encoding/json"
	"fmt"
	"net"
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
	b = append(b, '\n')
	conn, err := net.DialTimeout("unix", filepath.Join(dir, "sock"), 5*time.Millisecond)
	if err == nil {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Millisecond))
		_, err = conn.Write(b)
		_ = conn.Close()
		if err == nil {
			return nil
		}
	}
	return appendSpool(dir, ev)
}
