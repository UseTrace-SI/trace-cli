//go:build linux

package daemon

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// A stale pid reused by a kernel thread has an empty command line; it is
// not the daemon, so stop and uninstall may discard the pid file.
func TestCheckTraceDaemonEmptyCommandLine(t *testing.T) {
	saved := readCommandLine
	readCommandLine = func(int) (string, error) { return "", nil }
	defer func() { readCommandLine = saved }()
	if got := CheckTraceDaemon(os.Getpid(), t.TempDir()); got != DaemonAbsent {
		t.Fatalf("empty command line = %v, want absent", got)
	}
	if command, err := processCommandLine(2); err != nil && !os.IsNotExist(err) && command != "" {
		t.Fatalf("kthreadd command line = %q, %v", command, err)
	}
}

func TestCheckTraceDaemonZombie(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Wait() }()
	pid := cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for !processZombie(pid) {
		if time.Now().After(deadline) {
			t.Fatal("child never became a zombie")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := CheckTraceDaemon(pid, t.TempDir()); got != DaemonAbsent {
		t.Fatalf("zombie = %v, want absent", got)
	}
	if processZombie(os.Getpid()) {
		t.Fatal("running test process reported as zombie")
	}
}
