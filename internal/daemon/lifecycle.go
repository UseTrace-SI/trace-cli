package daemon

import (
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

func socketReady(dir string) bool {
	conn, err := net.DialTimeout("unix", filepath.Join(dir, "sock"), 5*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
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

// EnsureRunning starts the daemon and waits until it is accepting events.
func EnsureRunning(root string) error {
	dir := filepath.Join(root, ".trace")
	if SpoolReady(dir) || socketReady(dir) {
		return nil
	}
	if err := startProcess(root); err != nil {
		return err
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
// blocking the agent.
func StartAsync(root string) {
	dir := filepath.Join(root, ".trace")
	if SpoolReady(dir) || socketReady(dir) {
		return
	}
	_ = startProcess(root)
}

func SpoolReady(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "spool.ready")); err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, "daemon.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil || pid <= 0 {
		return false
	}
	err = syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
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
// daemon.pid file. The daemon is started as "<trace> --repo <root> daemon".
func ProcessIsTraceDaemon(pid int, root string) bool {
	if !ProcessRunning(pid) {
		return false
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := strings.TrimSpace(string(out))
	return strings.Contains(command, "--repo "+root) && strings.HasSuffix(command, " daemon")
}
