package daemon

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// syncBuffer lets the test read log output while the server goroutine writes it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func TestQuarantineFrameRedactsBeforeWriting(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz012345"
	for _, tc := range []struct {
		name, frame string
	}{
		{"malformed with secret", `{invalid "token":"` + secret + `"}`},
		{"plain malformed", `{invalid`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := quarantineFrame(dir, []byte(tc.frame)); err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(dir, "quarantine", "*.frame"))
			if err != nil || len(files) != 1 {
				t.Fatalf("quarantined files = %v, %v", files, err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), secret) {
				t.Fatalf("secret was persisted in quarantine: %q", data)
			}
			info, err := os.Stat(files[0])
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("quarantine mode = %v, %v", info, err)
			}
			if strings.Contains(redactPreview([]byte(tc.frame)), secret) {
				t.Fatalf("secret leaked into diagnostic: %q", redactPreview([]byte(tc.frame)))
			}
		})
	}
}

func TestServeQuarantinesUndecodableSocketFrame(t *testing.T) {
	dir := t.TempDir()
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeCheckedWithLogger(ctx, dir, logger, func(event.Event) error { return nil })
	}()
	// The platform-unavailable branch below consumes `done` itself, so register
	// the shutdown assertion only after socket readiness is established. That
	// keeps a clean t.Skip from reporting a second read of the same channel.
	sock := filepath.Join(dir, "sock")
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if _, err := os.Stat(filepath.Join(dir, "spool.ready")); err == nil {
			cancel()
			<-done
			t.Skip("Unix socket binding is unavailable in this sandbox")
		}
		time.Sleep(5 * time.Millisecond)
	}
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
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	secret := "ghp_abcdefghijklmnopqrstuvwxyz012345"
	if _, err := conn.Write([]byte(`{invalid "key":"` + secret + `"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	files, err := filepath.Glob(filepath.Join(dir, "quarantine", "*.frame"))
	for deadline := time.Now().Add(2 * time.Second); len(files) == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
		files, err = filepath.Glob(filepath.Join(dir, "quarantine", "*.frame"))
	}
	if err != nil || len(files) != 1 {
		t.Fatalf("invalid frame was not quarantined: %v, %v", files, err)
	}
	if !strings.Contains(logs.String(), "discarding invalid socket frame") {
		t.Fatalf("no diagnostic logged: %q", logs.String())
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("secret leaked into daemon log: %q", logs.String())
	}
}

// TestServeFallbackShutdownWithLogger covers the path taken when Unix socket
// binding is denied: the daemon must still start, ingest from the spool, and
// stop cleanly with exactly one consumer of its result channel.
func TestServeFallbackShutdownWithLogger(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("x", 120))
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeCheckedWithLogger(ctx, dir, logger, func(event.Event) error { return nil })
	}()
	ready := filepath.Join(dir, "spool.ready")
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(ready); err != nil {
		cancel()
		<-done
		t.Fatalf("spool fallback did not become ready: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stopping daemon: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not stop in spool fallback mode")
	}
}

// Quarantined socket frames must be bounded just like quarantined spool
// frames: an invalid-frame storm must not grow the directory without limit.
func TestQuarantineFrameIsBounded(t *testing.T) {
	dir := t.TempDir()
	qdir := filepath.Join(dir, "quarantine")
	if err := os.MkdirAll(qdir, 0700); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-spoolRetention - time.Hour)
	// Three stale frames and (maxQuarantinedFrames-1) fresh frames. The next
	// quarantine pushes the directory over budget, so pruning must remove the
	// stale frames plus the oldest fresh one and leave exactly the budget.
	for i := 0; i < maxQuarantinedFrames+2; i++ {
		path := filepath.Join(qdir, fmt.Sprintf("frame-%03d.frame", i))
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		when := time.Now()
		if i < 3 {
			when = stale
		}
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	if err := quarantineFrame(dir, []byte(`{invalid`)); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(qdir, "*.frame"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != maxQuarantinedFrames {
		t.Fatalf("quarantined frames = %d, want %d", len(files), maxQuarantinedFrames)
	}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(info.ModTime()) > spoolRetention {
			t.Fatalf("stale quarantined frame survived pruning: %s", path)
		}
		if base := filepath.Base(path); base == "frame-000.frame" || base == "frame-001.frame" || base == "frame-002.frame" {
			t.Fatalf("stale quarantined frame survived pruning: %s", path)
		}
	}
}
