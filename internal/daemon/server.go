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
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENAMETOOLONG) {
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
			var ev event.Event
			if json.Unmarshal(s.Bytes(), &ev) == nil {
				if err := delivery.socket(ev); err != nil {
					acceptErr = fmt.Errorf("delivering socket event: %w", err)
					break
				}
			}
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

// orderedDelivery keeps a completed fallback write ahead of later socket events.
type orderedDelivery struct {
	mu    sync.Mutex
	dir   string
	queue chan<- event.Event
}

func (d *orderedDelivery) drain() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ingestSpool(d.dir, func(ev event.Event) { d.queue <- ev })
}

func (d *orderedDelivery) socket(ev event.Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ingestSpool(d.dir, func(spooled event.Event) { d.queue <- spooled }); err != nil {
		return err
	}
	d.queue <- ev.Sanitized()
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
	return pollSpool(ctx, func() error { return ingestSpool(dir, consume) })
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
