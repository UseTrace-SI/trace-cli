package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
)

func TestAutoGCDue(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	for _, tc := range []struct {
		name         string
		last         time.Time
		size, budget int64
		want         bool
	}{
		{"never ran", time.Time{}, 0, 100, true},
		{"ran an hour ago", now.Add(-time.Hour), 0, 100, false},
		{"ran a day ago", now.Add(-autoGCInterval), 0, 100, true},
		{"over budget, ran recently", now.Add(-time.Minute), 200, 100, false},
		{"over budget, ran an hour ago", now.Add(-autoGCOverBudgetEvery), 200, 100, true},
		{"at budget", now.Add(-2 * time.Hour), 100, 100, false},
		{"clock moved back", now.Add(time.Hour), 0, 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := autoGCDue(tc.last, tc.size, tc.budget, now); got != tc.want {
				t.Fatalf("autoGCDue = %v; want %v", got, tc.want)
			}
		})
	}
}

// autoGCRepo returns a repository whose step 1 expired long ago, beside a
// recent pre-rewind undo step and a recent turn.
func autoGCRepo(t *testing.T) (string, *engine.Engine) {
	t.Helper()
	t.Setenv("TRACE_HOME", t.TempDir())
	t.Setenv("TRACE_MAX_STORE_BYTES", "")
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	if _, err := e.DB.Exec(`
INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) SELECT 'expired',1,manifest_hash,'turn',1 FROM keyframes WHERE seq=0;
INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) SELECT 'undo',2,manifest_hash,'pre-rewind',? FROM keyframes WHERE seq=0;
INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) SELECT 'recent',3,manifest_hash,'turn',? FROM keyframes WHERE seq=0;`, now, now); err != nil {
		t.Fatal(err)
	}
	return root, e
}

func keyframeIDs(t *testing.T, e *engine.Engine) string {
	t.Helper()
	rows, err := e.DB.Query("SELECT id FROM keyframes WHERE tag IS NULL OR tag!='gc-safety' ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(ids, ",")
}

func TestAutoGCRunsDailyWithGCRetention(t *testing.T) {
	root, e := autoGCRepo(t)
	baseline := keyframeIDs(t, e)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	now := time.Now()
	ran, err := runAutoGC(&autoGCState{}, root, e, logger, now)
	if err != nil || !ran {
		t.Fatalf("first automatic GC: ran %v, %v", ran, err)
	}
	if got, want := keyframeIDs(t, e), strings.Replace(baseline, "expired,", "", 1); got != want {
		t.Fatalf("keyframes after automatic GC = %s; want %s (only the expired step removed)", got, want)
	}
	if !strings.Contains(logs.String(), "automatic gc") {
		t.Fatalf("collection not logged: %s", logs.String())
	}
	// A second expired step within the day waits for the next window.
	if _, err := e.DB.Exec("INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) SELECT 'later',100,manifest_hash,'turn',1 FROM keyframes WHERE seq=0"); err != nil {
		t.Fatal(err)
	}
	if ran, err := runAutoGC(&autoGCState{}, root, e, logger, now.Add(time.Hour)); err != nil || ran {
		t.Fatalf("automatic GC ran again within a day: %v, %v", ran, err)
	}
	if !strings.Contains(keyframeIDs(t, e), "later") {
		t.Fatal("expired step collected before the next window")
	}
	if ran, err := runAutoGC(&autoGCState{}, root, e, logger, now.Add(autoGCInterval)); err != nil || !ran {
		t.Fatalf("automatic GC did not run a day later: %v, %v", ran, err)
	}
	if strings.Contains(keyframeIDs(t, e), "later") {
		t.Fatal("expired step survived the next window")
	}
}

func TestAutoGCSkipsSafetyKeyframeWhenNothingExpired(t *testing.T) {
	root, e := autoGCRepo(t)
	if _, err := e.DB.Exec("DELETE FROM keyframes WHERE id='expired'"); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM keyframes").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if ran, err := runAutoGC(&autoGCState{}, root, e, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now()); err != nil || ran {
		t.Fatalf("ran %v, %v", ran, err)
	}
	var after int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM keyframes").Scan(&after); err != nil || after != before {
		t.Fatalf("idle automatic GC added keyframes: %d -> %d, %v", before, after, err)
	}
}

func TestAutoGCBudgetBringsCollectionForward(t *testing.T) {
	root, e := autoGCRepo(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := filepath.Join(root, ".trace")
	recent := time.Now().Add(-2 * time.Hour)
	if err := os.WriteFile(filepath.Join(dir, autoGCStateName), []byte(strconv.FormatInt(recent.UnixMilli(), 10)), 0600); err != nil {
		t.Fatal(err)
	}
	if ran, err := runAutoGC(&autoGCState{}, root, e, logger, time.Now()); err != nil || ran {
		t.Fatalf("under budget, ran %v within the day: %v", ran, err)
	}
	t.Setenv("TRACE_MAX_STORE_BYTES", "1")
	if ran, err := runAutoGC(&autoGCState{}, root, e, logger, time.Now()); err != nil || !ran {
		t.Fatalf("over budget, automatic GC did not run: %v, %v", ran, err)
	}
	if strings.Contains(keyframeIDs(t, e), "expired") {
		t.Fatal("expired step survived a budget-triggered collection")
	}
	if !strings.Contains(keyframeIDs(t, e), "undo") || !strings.Contains(keyframeIDs(t, e), "recent") {
		t.Fatal("the budget made collection more aggressive than trace gc")
	}
	t.Setenv("TRACE_MAX_STORE_BYTES", "-5")
	if _, err := runAutoGC(&autoGCState{}, root, e, logger, time.Now()); err == nil {
		t.Fatal("invalid budget accepted")
	}
}

// Collection removes turns and their pending sync updates. With sync
// configured, history that has not been uploaded must not vanish unattended.
func TestAutoGCSkippedWhileSyncIsConfigured(t *testing.T) {
	root, e := autoGCRepo(t)
	login, err := json.Marshal(syncclient.Config{Endpoint: "https://trace.example", Remote: "owner/repo", Mode: "pro"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loginPath(root), login, 0600); err != nil {
		t.Fatal(err)
	}
	if ran, err := runAutoGC(&autoGCState{}, root, e, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now()); err != nil || ran {
		t.Fatalf("ran %v with sync configured: %v", ran, err)
	}
	if !strings.Contains(keyframeIDs(t, e), "expired") {
		t.Fatal("automatic GC removed history while sync is configured")
	}
}

func TestAutoGCCanBeDisabled(t *testing.T) {
	for value, want := range map[string]bool{"": true, "1": true, "0": false, "off": false, "FALSE": false} {
		t.Setenv("TRACE_AUTO_GC", value)
		if got := autoGCEnabled(); got != want {
			t.Errorf("TRACE_AUTO_GC=%q: enabled %v; want %v", value, got, want)
		}
	}
}

func TestAutoGCLoopStopsWithTheDaemon(t *testing.T) {
	t.Setenv("TRACE_AUTO_GC", "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		autoGCLoop(ctx, t.TempDir(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("automatic GC loop ignored cancellation")
	}
}

// A store over budget with nothing old enough to collect is checked hourly;
// its warning is logged once, and again only after the condition clears.
func TestAutoGCOverBudgetWarningIsLoggedOncePerChange(t *testing.T) {
	root, e := autoGCRepo(t)
	if _, err := e.DB.Exec("DELETE FROM keyframes WHERE id='expired'"); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	var state autoGCState
	const warning = "no history is old enough to collect"
	t.Setenv("TRACE_MAX_STORE_BYTES", "1")
	now := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := runAutoGC(&state, root, e, logger, now.Add(time.Duration(i)*autoGCOverBudgetEvery)); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(logs.String(), warning); n != 1 {
		t.Fatalf("warning logged %d times over three hourly checks; want 1:\n%s", n, logs.String())
	}
	t.Setenv("TRACE_MAX_STORE_BYTES", "")
	if _, err := runAutoGC(&state, root, e, logger, now.Add(3*autoGCOverBudgetEvery)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_MAX_STORE_BYTES", "1")
	if _, err := runAutoGC(&state, root, e, logger, now.Add(4*autoGCOverBudgetEvery)); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(logs.String(), warning); n != 2 {
		t.Fatalf("warning logged %d times after the condition recurred; want 2", n)
	}
}
