package daemon

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestHelperDaemonProcess is a stand-in daemon: started with
// "--repo ROOT daemon" arguments, it waits to be killed.
func TestHelperDaemonProcess(t *testing.T) {
	if os.Getenv("TRACE_TEST_FAKE_DAEMON") != "1" {
		t.Skip("helper process")
	}
	time.Sleep(time.Minute)
}

func startFakeDaemon(t *testing.T, root string) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperDaemonProcess$", "--", "--repo", root, "daemon")
	cmd.Env = append(os.Environ(), "TRACE_TEST_FAKE_DAEMON=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	// Wait until the process has exec'd and its command line is visible.
	for i := 0; i < 200; i++ {
		if command, err := processCommandLine(cmd.Process.Pid); err == nil && strings.HasSuffix(command, " daemon") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cmd.Process.Pid
}

func TestCheckTraceDaemonWithoutPs(t *testing.T) {
	root := t.TempDir()
	pid := startFakeDaemon(t, root)
	t.Setenv("PATH", t.TempDir()) // no ps here

	if runtime.GOOS == "linux" {
		// /proc identifies the daemon without ps.
		if got := CheckTraceDaemon(pid, root); got != DaemonConfirmed {
			t.Fatalf("CheckTraceDaemon without ps = %v, want confirmed", got)
		}
		if got := CheckTraceDaemon(pid, t.TempDir()); got != DaemonAbsent {
			t.Fatalf("another repository's daemon = %v, want absent", got)
		}
	}

	// Where identification needs ps, a missing ps is unknown, not absent:
	// callers must keep the pid file and not claim the daemon stopped.
	saved := readCommandLine
	readCommandLine = psCommandLine
	defer func() { readCommandLine = saved }()
	if got := CheckTraceDaemon(pid, root); got != DaemonUnknown {
		t.Fatalf("CheckTraceDaemon with failing ps = %v, want unknown", got)
	}
	if ProcessIsTraceDaemon(pid, root) {
		t.Fatal("an unidentified process was reported as the daemon")
	}
}

func TestCheckTraceDaemonAbsentProcess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if got := CheckTraceDaemon(cmd.Process.Pid, t.TempDir()); got != DaemonAbsent {
		t.Fatalf("exited process = %v, want absent", got)
	}
}
