package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDaemonLockSerializesStartup(t *testing.T) {
	dir := t.TempDir()
	lock, acquired, err := TryAcquireDaemonLock(dir)
	if err != nil || !acquired {
		t.Fatalf("first lock: acquired=%t err=%v", acquired, err)
	}
	if _, again, err := TryAcquireDaemonLock(dir); err != nil || again {
		t.Fatalf("second lock should be contended: acquired=%t err=%v", again, err)
	}
	if !daemonLockHeld(dir) {
		t.Fatal("held lock was not detected")
	}
	ReleaseDaemonLock(lock)
	if daemonLockHeld(dir) {
		t.Fatal("released lock still appears held")
	}
	lock, acquired, err = TryAcquireDaemonLock(dir)
	if err != nil || !acquired {
		t.Fatalf("lock after release: acquired=%t err=%v", acquired, err)
	}
	ReleaseDaemonLock(lock)
}

func TestDaemonIdentityAndStartTime(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDaemonIdentity(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, daemonPIDName))
	if err != nil || string(b) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("pid identity = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, daemonStartTime)); err != nil {
		t.Fatalf("start time was not recorded: %v", err)
	}
	RemoveDaemonIdentity(dir)
	for _, name := range []string{daemonPIDName, daemonStartTime} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was not removed: %v", name, err)
		}
	}
}

func TestSpoolReadyRejectsRecycledPID(t *testing.T) {
	if _, ok := processStartTime(os.Getpid()); !ok {
		t.Skip("process start time is unavailable on this platform")
	}
	dir := t.TempDir()
	for name, data := range map[string]string{
		"spool.ready":   "1\n",
		daemonPIDName:   strconv.Itoa(os.Getpid()),
		daemonStartTime: "1", // deliberately wrong: simulates a reused PID
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if SpoolReady(dir) {
		t.Fatal("a mismatched process start time was accepted")
	}
	start, _ := processStartTime(os.Getpid())
	if err := os.WriteFile(filepath.Join(dir, daemonStartTime), []byte(strconv.FormatUint(start, 10)), 0600); err != nil {
		t.Fatal(err)
	}
	if !SpoolReady(dir) {
		t.Fatal("a live daemon identity was rejected")
	}
}

func TestProcessIsTraceDaemonChecksStartTime(t *testing.T) {
	if _, ok := processStartTime(os.Getpid()); !ok {
		t.Skip("process start time is unavailable on this platform")
	}
	root := t.TempDir()
	traceDir := filepath.Join(root, ".trace")
	if err := os.MkdirAll(traceDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(traceDir, daemonStartTime), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	if ProcessIsTraceDaemon(os.Getpid(), root) {
		t.Fatal("a stale process start time was accepted")
	}
}
