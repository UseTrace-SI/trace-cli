package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func Serve(ctx context.Context, dir string, consume func(event.Event)) error {
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
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			return serveSpool(ctx, dir, consume)
		}
		return fmt.Errorf("listening on socket: %w", err)
	}
	defer ln.Close()
	defer os.Remove(sock)
	if err := os.Chmod(sock, 0600); err != nil {
		return fmt.Errorf("setting socket mode: %w", err)
	}
	queue := make(chan event.Event, 1024)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for ev := range queue {
			consume(ev)
		}
	}()
	if err := ingestSpool(dir, func(ev event.Event) { queue <- ev }); err != nil {
		close(queue)
		workers.Wait()
		return err
	}
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			close(queue)
			workers.Wait()
			return fmt.Errorf("accepting event: %w", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		s := bufio.NewScanner(conn)
		s.Buffer(make([]byte, 4096), 4<<20)
		for s.Scan() {
			var ev event.Event
			if json.Unmarshal(s.Bytes(), &ev) == nil {
				queue <- ev.Sanitized()
			}
		}
		conn.Close()
	}
	close(queue)
	workers.Wait()
	return nil
}

// serveSpool keeps local capture usable where Unix socket binding is denied.
// Hooks still use their bounded socket attempt and append-only spool fallback.
func serveSpool(ctx context.Context, dir string, consume func(event.Event)) error {
	ready := filepath.Join(dir, "spool.ready")
	if err := os.WriteFile(ready, []byte("1\n"), 0600); err != nil {
		return fmt.Errorf("marking spool daemon ready: %w", err)
	}
	defer os.Remove(ready)
	if err := ingestSpool(dir, consume); err != nil {
		return err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := ingestSpool(dir, consume); err != nil {
				return err
			}
		}
	}
}
