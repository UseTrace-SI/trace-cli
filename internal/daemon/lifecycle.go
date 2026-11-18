package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	daemonLockName  = "daemon.lock"
	daemonPIDName   = "daemon.pid"
	daemonStartTime = "daemon.start"
)

func socketReady(dir string) bool {
	conn, err := net.DialTimeout("unix", filepath.Join(dir, "sock"), 5*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func openDaemonLock(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating trace directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, daemonLockName), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening daemon lock: %w", err)
	}
	return f, nil
}

// TryAcquireDaemonLock takes the single lock shared by daemon startup and the
// daemon process itself. A false result means another process already holds it
// (a daemon is running or starting), never that startup failed.
func TryAcquireDaemonLock(dir string) (*os.File, bool, error) {
	f, err := openDaemonLock(dir)
	if err != nil {
		return nil, false, err
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return f, true, nil
	}
	f.Close()
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("locking daemon: %w", err)
}

func ReleaseDaemonLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// daemonLockHeld reports whether some live process holds the daemon lock.
// Trying the nonblocking lock avoids trusting a PID that may have been reused.
func daemonLockHeld(dir string) bool {
	f, err := openDaemonLock(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false
	}
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func startProcess(root string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating trace executable: %w", err)
	}
	cmd := exec.Command(exe, "--repo", root, "daemon")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("releasing daemon: %w", err)
	}
	return nil
}

// WriteDaemonIdentity records the PID and, where available, the process start
// time so a stale pid file cannot be mistaken for a live daemon after PID reuse.
func WriteDaemonIdentity(dir string) error {
	pid := os.Getpid()
	if err := os.WriteFile(filepath.Join(dir, daemonPIDName), []byte(strconv.Itoa(pid)), 0600); err != nil {
		return fmt.Errorf("writing pid file: %w", err)
	}
	if start, ok := processStartTime(pid); ok {
		if err := os.WriteFile(filepath.Join(dir, daemonStartTime), []byte(strconv.FormatUint(start, 10)), 0600); err != nil {
			return fmt.Errorf("writing daemon start time: %w", err)
		}
	}
	return nil
}

func RemoveDaemonIdentity(dir string) {
	_ = os.Remove(filepath.Join(dir, daemonPIDName))
	_ = os.Remove(filepath.Join(dir, daemonStartTime))
}

func readDaemonStartTime(dir string) uint64 {
	b, err := os.ReadFile(filepath.Join(dir, daemonStartTime))
	if err != nil {
		return 0
	}
	start, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return start
}

// PersonalSyncReady identifies support in the running daemon, rather than
// assuming a newly installed CLI has replaced an older resident process.
func PersonalSyncReady(dir string) bool {
	capability, err := os.ReadFile(filepath.Join(dir, "personal-sync.ready"))
	if err != nil || len(capability) == 0 {
		return false
	}
	pid, err := os.ReadFile(filepath.Join(dir, daemonPIDName))
	return err == nil && string(pid) == string(capability)
}

// EnsureRunning starts the daemon and waits until it is accepting events. The
// decision to start is made while holding the daemon lock, so two callers
// cannot both spawn a process.
func EnsureRunning(root string) error {
	dir := filepath.Join(root, ".trace")
	if SpoolReady(dir) || socketReady(dir) {
		return nil
	}
	lock, acquired, err := TryAcquireDaemonLock(dir)
	if err != nil {
		return err
	}
	if acquired {
		if SpoolReady(dir) || socketReady(dir) {
			ReleaseDaemonLock(lock)
			return nil
		}
		err = startProcess(root)
		// Release before polling: the new daemon needs the same lock.
		ReleaseDaemonLock(lock)
		if err != nil {
			return err
		}
	}
	for i := 0; i < 100; i++ {
		if SpoolReady(dir) || socketReady(dir) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start")
}

// StartAsync launches the daemon without waiting for it to become ready. Hooks
// use it on SessionStart so an idle-exited or crashed daemon recovers without
// blocking the agent. If another starter holds the lock, it does nothing.
func StartAsync(root string) {
	dir := filepath.Join(root, ".trace")
	if SpoolReady(dir) || socketReady(dir) {
		return
	}
	lock, acquired, err := TryAcquireDaemonLock(dir)
	if err != nil || !acquired {
		return
	}
	defer ReleaseDaemonLock(lock)
	if SpoolReady(dir) || socketReady(dir) {
		return
	}
	_ = startProcess(root)
}

func SpoolReady(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "spool.ready")); err != nil {
		return false
	}
	// A live daemon holds its lock for the whole process lifetime. This is
	// authoritative even if the pid file points at a recycled PID.
	if daemonLockHeld(dir) {
		return true
	}
	b, err := os.ReadFile(filepath.Join(dir, daemonPIDName))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	if expected := readDaemonStartTime(dir); expected > 0 {
		actual, ok := processStartTime(pid)
		return ok && actual == expected
	}
	// Fallback for daemons started before start-time markers existed.
	return ProcessRunning(pid)
}

// ProcessRunning reports whether pid still exists.
func ProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// ProcessIsTraceDaemon guards against signalling a recycled PID from a stale
// daemon.pid file. A recorded process start time identifies the exact process
// instance; the command-line check remains as a fallback for older daemons.
func ProcessIsTraceDaemon(pid int, root string) bool {
	if !ProcessRunning(pid) {
		return false
	}
	if expected := readDaemonStartTime(filepath.Join(root, ".trace")); expected > 0 {
		actual, ok := processStartTime(pid)
		if !ok || actual != expected {
			return false
		}
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := strings.TrimSpace(string(out))
	return strings.Contains(command, "--repo "+root) && strings.HasSuffix(command, " daemon")
}
