package companions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "companion")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCompanionProtocolBoundsAndCancellation(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ name, body, want string }{
		{"ready", `printf '{"protocol":1,"status":"ready"}'`, "ready"},
		{"old", `printf '{"protocol":0,"status":"ready"}'`, "unsupported_protocol"},
		{"invalid", `printf 'not JSON'`, "unsupported_protocol"},
		{"missing", `printf '{"protocol":1,"status":"missing_index"}'`, "missing_index"},
		{"failure", `echo 'ghp_abcdefghijklmnopqrstuvwxyz' >&2; exit 1`, "failed"},
		{"flood", `dd if=/dev/zero bs=1048576 count=2 2>/dev/null`, "output_limit"},
		// The child inherits stdout. Cancelling only the shell would hang.
		{"child", `sleep 30 & wait`, "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := 5 * time.Second
			if tc.name == "child" {
				budget = 30 * time.Millisecond
			}
			start := time.Now()
			got := run(context.Background(), root, script(t, tc.body), budget, nil)
			if got.Status != tc.want {
				t.Fatalf("got %s, want %s", got.Status, tc.want)
			}
			if time.Since(start) > budget+1500*time.Millisecond {
				t.Fatal("subprocess or inherited stdout was not bounded")
			}
		})
	}
}

func TestCompanionWorkerCoalescesWithoutBlockingCapture(t *testing.T) {
	var latest atomic.Int64
	seen := make(chan int64, 3)
	release := make(chan struct{})
	w := start(context.Background(), func(ctx context.Context) {
		seen <- latest.Load()
		select {
		case <-release:
		case <-ctx.Done():
		}
	}, 0, 0)
	defer w.Close()
	select {
	case <-seen:
	case <-time.After(time.Second):
		t.Fatal("startup catch-up did not run")
	}
	notified := make(chan struct{})
	go func() {
		for i := int64(1); i <= 10000; i++ {
			latest.Store(i)
			w.Notify()
		}
		close(notified)
	}()
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("capture waited for a companion refresh")
	}
	close(release)
	select {
	case n := <-seen:
		if n != 10000 {
			t.Fatalf("lost latest generation: %d", n)
		}
	case <-time.After(time.Second):
		t.Fatal("pending refresh was lost")
	}
	w.Close()
	if len(seen) != 0 {
		t.Fatal("burst created redundant refresh jobs")
	}
}

func TestCompanionPathsAndStatus(t *testing.T) {
	t.Setenv("TRACE_COMPANIONS", "1")
	root := t.TempDir()
	if got := DBPath(root, "brain"); got != filepath.Join(root, ".trace", "brain", "memory.db") {
		t.Fatal(got)
	}
	legacy := filepath.Join(root, ".trace-brain", "memory.db")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if DBPath(root, "brain") != legacy {
		t.Fatal("existing memories were abandoned")
	}
	report := Report{Protocol: 1, UpdatedAt: 1, Brain: State{Status: "ready"}, Graph: State{Status: "not_installed"}}
	if err := SaveStatus(root, report); err == nil {
		t.Fatal("status recreated missing history")
	}
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".trace", "trace.db"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveStatus(root, report); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, ".trace", "companions", "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Report
	if json.Unmarshal(b, &got) != nil || got != report {
		t.Fatalf("bad status: %s", b)
	}
	if !AutoEnabled(root) {
		t.Fatal("default refresh disabled")
	}
	if err := os.WriteFile(filepath.Join(root, ".trace", "companions", "disabled"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if AutoEnabled(root) {
		t.Fatal("disable-auto ignored")
	}
}
