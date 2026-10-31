// Package companions implements the bounded protocol between Trace and its
// optional Brain/Graph executables. Capture never depends on their availability.
package companions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const protocol = 1
const maxOutput = 1 << 20

type State struct {
	Status string `json:"status"`
}

type Report struct {
	Protocol  int   `json:"protocol"`
	UpdatedAt int64 `json:"updated_at"`
	Brain     State `json:"brain"`
	Graph     State `json:"graph"`
}

type Memory struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	Content   string `json:"content"`
	Source    string `json:"source"`
	SessionID string `json:"session_id,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type Symbol struct {
	ID        string `json:"id"`
	File      string `json:"file"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Line      int    `json:"line"`
	Signature string `json:"signature"`
}

type Relation struct {
	From string `json:"from_symbol"`
	To   string `json:"to_symbol"`
	Kind string `json:"kind"`
	File string `json:"file"`
	Line int    `json:"line"`
}

type MemoryContext struct {
	Protocol int      `json:"protocol"`
	Status   string   `json:"status"`
	Memories []Memory `json:"memories"`
}

type CodeContext struct {
	Protocol  int        `json:"protocol"`
	Status    string     `json:"status"`
	Repo      string     `json:"repo"`
	IndexedAt int64      `json:"indexed_at,omitempty"`
	Symbols   []Symbol   `json:"symbols"`
	Relations []Relation `json:"relations"`
}

// DBPath preserves a pre-existing standalone database. New integrated state
// lives under .trace, alongside the history from which it can be rebuilt.
func DBPath(root, name string) string {
	legacy := filepath.Join(root, ".trace-"+name, map[string]string{"brain": "memory.db", "graph": "graph.db"}[name])
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		return legacy
	}
	file := "graph.db"
	if name == "brain" {
		file = "memory.db"
	}
	return filepath.Join(root, ".trace", name, file)
}

type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	cancel   context.CancelFunc
	exceeded bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buf.Len() {
		b.exceeded = true
		b.cancel()
		return 0, fmt.Errorf("companion output exceeds limit")
	}
	return b.buf.Write(p)
}

// run never starts a shell, exposes subprocess output in errors, or leaves a
// child's process group holding output pipes after cancellation.
func run(ctx context.Context, root, program string, timeout time.Duration, reply any, args ...string) State {
	exe, err := exec.LookPath(program)
	if err != nil {
		return State{Status: "not_installed"}
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return State{Status: "failed"}
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	stdout := &cappedBuffer{limit: maxOutput, cancel: cancel}
	stderr := &cappedBuffer{limit: 4096, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if stdout.exceeded || stderr.exceeded {
			return State{Status: "output_limit"}
		}
		if parent.Err() != nil {
			return State{Status: "cancelled"}
		}
		if ctx.Err() == context.DeadlineExceeded {
			return State{Status: "timeout"}
		}
		return State{Status: "failed"}
	}
	var header struct {
		Protocol int    `json:"protocol"`
		Status   string `json:"status"`
	}
	if json.Unmarshal(stdout.buf.Bytes(), &header) != nil || header.Protocol != protocol {
		return State{Status: "unsupported_protocol"}
	}
	if header.Status != "ready" && header.Status != "missing_index" && header.Status != "needs_refresh" {
		return State{Status: "invalid_response"}
	}
	if reply != nil && json.Unmarshal(stdout.buf.Bytes(), reply) != nil {
		return State{Status: "invalid_response"}
	}
	return State{Status: header.Status}
}

func Refresh(ctx context.Context, root string) Report {
	out := Report{Protocol: protocol}
	var err error
	root, err = canonicalRoot(root)
	if err != nil {
		out.Brain, out.Graph = State{Status: "failed"}, State{Status: "failed"}
		out.UpdatedAt = time.Now().UnixMilli()
		return out
	}
	out.Brain = run(ctx, root, "trace-brain", time.Minute, nil, "--db", DBPath(root, "brain"), "refresh", "--trace-db", filepath.Join(root, ".trace", "trace.db"))
	out.Graph = run(ctx, root, "trace-graph", time.Minute, nil, "--db", DBPath(root, "graph"), "--repo", root, "refresh")
	out.UpdatedAt = time.Now().UnixMilli()
	return out
}

func AutoEnabled(root string) bool {
	if os.Getenv("TRACE_COMPANIONS") == "0" {
		return false
	}
	_, err := os.Stat(filepath.Join(root, ".trace", "companions", "disabled"))
	return os.IsNotExist(err)
}

func Lookup(ctx context.Context, root, path, query string, limit int) (MemoryContext, CodeContext) {
	brain := MemoryContext{Protocol: protocol, Memories: []Memory{}}
	code := CodeContext{Protocol: protocol, Symbols: []Symbol{}, Relations: []Relation{}}
	var err error
	root, err = canonicalRoot(root)
	if err != nil {
		brain.Status, code.Status = "failed", "failed"
		return brain, code
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	brain.Status = run(ctx, root, "trace-brain", 2*time.Second, &brain, "--db", DBPath(root, "brain"), "context", "--trace-db", filepath.Join(root, ".trace", "trace.db"), "--query", query, "--limit", strconv.Itoa(limit)).Status
	code.Status = run(ctx, root, "trace-graph", 2*time.Second, &code, "--db", DBPath(root, "graph"), "--repo", root, "context", "--path", path, "--query", query, "--limit", strconv.Itoa(limit)).Status
	// Bound even a misbehaving executable that claims the supported protocol.
	if len(brain.Memories) > limit {
		brain.Status = "invalid_response"
	}
	if len(code.Symbols) > limit || len(code.Relations) > 50 || (code.Status == "ready" && code.Repo != root) {
		code.Status = "invalid_response"
	}
	if brain.Status != "ready" {
		brain.Memories = []Memory{}
	}
	if code.Status != "ready" {
		code.Symbols, code.Relations = []Symbol{}, []Relation{}
	}
	return brain, code
}

func canonicalRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// SaveStatus only writes safe operation metadata, never companion content.
func SaveStatus(root string, report Report) error {
	if _, err := os.Stat(filepath.Join(root, ".trace", "trace.db")); err != nil {
		return err
	}
	dir := filepath.Join(root, ".trace", "companions")
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	f, err := os.CreateTemp(dir, ".status-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(report)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(dir, "status.json"))
}

// Worker coalesces completed-turn notifications. Notify never waits for a
// refresh, so companion work stays outside event capture and agent hooks.
type Worker struct {
	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func Start(ctx context.Context, refresh func(context.Context)) *Worker {
	return start(ctx, refresh, 250*time.Millisecond, 0)
}

// StartDelayed is like Start but waits initialDelay before the first refresh,
// letting callers finish setup work without contending on companion databases.
func StartDelayed(ctx context.Context, refresh func(context.Context), initialDelay time.Duration) *Worker {
	return start(ctx, refresh, 250*time.Millisecond, initialDelay)
}

func start(ctx context.Context, refresh func(context.Context), delay time.Duration, initialDelay time.Duration) *Worker {
	ctx, cancel := context.WithCancel(ctx)
	w := &Worker{wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		if initialDelay > 0 {
			timer := time.NewTimer(initialDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		w.Notify() // Catch up after a daemon restart or an interrupted refresh.
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.wake:
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			select {
			case <-w.wake:
			default:
			}
			refresh(ctx)
		}
	}()
	if initialDelay == 0 {
		w.Notify()
	}
	return w
}

func (w *Worker) Notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) Close() {
	w.once.Do(w.cancel)
	<-w.done
}
