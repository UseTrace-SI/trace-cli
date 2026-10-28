package daemon

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func EnsureRunning(root string) error {
	dir := filepath.Join(root, ".trace")
	if SpoolReady(dir) {
		return nil
	}
	conn, err := net.DialTimeout("unix", filepath.Join(dir, "sock"), 5*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil
	}
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
	for i := 0; i < 100; i++ {
		if SpoolReady(dir) {
			return nil
		}
		conn, err := net.DialTimeout("unix", filepath.Join(dir, "sock"), 5*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start")
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
