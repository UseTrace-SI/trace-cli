package cli

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
)

const (
	// Automatic collection uses trace gc's own retention and runs at most
	// once a day, or hourly while the store is over its budget.
	autoGCKeepDays        = 30
	autoGCInterval        = 24 * time.Hour
	autoGCOverBudgetEvery = time.Hour
	autoGCCheckEvery      = time.Hour
	autoGCStartDelay      = 10 * time.Minute
	// Objects younger than this may belong to a keyframe another Trace
	// process (a rewind, a fork) is still recording.
	autoGCMinBlobAge     = time.Hour
	defaultMaxStoreBytes = 5 << 30
	autoGCStateName      = "gc-auto.last"
)

// maxStoreBytes is the .trace size budget: TRACE_MAX_STORE_BYTES when set,
// 5 GiB otherwise. Exceeding it brings automatic GC forward and makes
// trace doctor warn.
func maxStoreBytes() (int64, error) {
	value := os.Getenv("TRACE_MAX_STORE_BYTES")
	if value == "" {
		return defaultMaxStoreBytes, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("TRACE_MAX_STORE_BYTES must be a positive byte count")
	}
	return n, nil
}

// autoGCEnabled reports whether the daemon may collect history on its own.
// TRACE_AUTO_GC=0 (or false/off) turns it off.
func autoGCEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TRACE_AUTO_GC"))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// traceDirSize sums the regular files under dir, skipping entries that
// vanish or cannot be read mid-walk.
func traceDirSize(dir string) int64 {
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return size
}

// autoGCDue decides whether an automatic collection should run now.
func autoGCDue(last time.Time, size, budget int64, now time.Time) (bool, string) {
	since := now.Sub(last)
	switch {
	case last.IsZero() || since < 0 || since >= autoGCInterval:
		return true, "daily"
	case size > budget && since >= autoGCOverBudgetEvery:
		return true, "store budget"
	}
	return false, ""
}

func readAutoGCLast(dir string) time.Time {
	b, err := os.ReadFile(filepath.Join(dir, autoGCStateName))
	if err != nil {
		return time.Time{}
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// runAutoGC performs one automatic collection if it is due. It reuses trace
// gc's retention and protections unchanged, adds a grace period for fresh
// objects, skips the safety keyframe and full scan when nothing has expired,
// and never runs while sync is configured, so history that has not been
// uploaded yet is never removed unattended.
func runAutoGC(root string, e *engine.Engine, logger *slog.Logger, now time.Time) (ran bool, err error) {
	dir := filepath.Join(root, ".trace")
	budget, err := maxStoreBytes()
	if err != nil {
		return false, err
	}
	size := traceDirSize(dir)
	due, reason := autoGCDue(readAutoGCLast(dir), size, budget, now)
	if !due {
		return false, nil
	}
	// Record the attempt first, so a failing collection is not retried hourly.
	if err := daemon.WriteStateFile(filepath.Join(dir, autoGCStateName), []byte(strconv.FormatInt(now.UnixMilli(), 10)+"\n")); err != nil {
		return false, fmt.Errorf("recording automatic GC: %w", err)
	}
	if _, err := readSyncConfig(root); err == nil || !os.IsNotExist(err) {
		logger.Info("automatic gc skipped: sync is configured; run trace gc after history is uploaded", "size_bytes", size, "budget_bytes", budget)
		return false, nil
	}
	preview, err := e.GCWith(engine.GCOptions{KeepDays: autoGCKeepDays, DryRun: true})
	if err != nil {
		return false, fmt.Errorf("previewing automatic GC: %w", err)
	}
	if preview.RemovedKeyframes == 0 {
		if size > budget {
			logger.Warn("trace store exceeds its budget but no history is old enough to collect", "size_bytes", size, "budget_bytes", budget, "keep_days", autoGCKeepDays)
		}
		return false, nil
	}
	result, err := e.GCWith(engine.GCOptions{KeepDays: autoGCKeepDays, MinBlobAge: autoGCMinBlobAge})
	if err != nil {
		return false, fmt.Errorf("automatic GC: %w", err)
	}
	logger.Info("automatic gc", "reason", reason, "removed_keyframes", result.RemovedKeyframes, "removed_blobs", result.RemovedBlobs, "removed_sessions", result.RemovedSessions, "size_bytes_before", size)
	return true, nil
}

// autoGCLoop checks hourly, after a startup delay, whether a collection is
// due. It returns when ctx is cancelled; a collection in progress finishes
// first.
func autoGCLoop(ctx context.Context, root string, e *engine.Engine, logger *slog.Logger) {
	if !autoGCEnabled() {
		return
	}
	timer := time.NewTimer(autoGCStartDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if _, err := runAutoGC(root, e, logger, time.Now()); err != nil {
			logger.Error("automatic gc", "error", err)
		}
		timer.Reset(autoGCCheckEvery)
	}
}
