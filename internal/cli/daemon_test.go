package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/daemon"
)

func TestRotatingLogRotatesWhileOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	log, err := openRotatingLog(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"123456", "7890", "x"} {
		if _, err := log.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "x" || string(previous) != "1234567890" {
		t.Fatalf("current=%q previous=%q", current, previous)
	}
}

func TestHelperStopTarget(t *testing.T) {
	if os.Getenv("TRACE_TEST_STOP_TARGET") != "1" {
		t.Skip("helper process")
	}
	time.Sleep(time.Minute)
}

// Without ps, Linux still identifies the daemon from /proc, so stop signals it
// instead of deleting daemon.pid and orphaning the process.
func TestDaemonStopWithoutPs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc is Linux-only")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0o700); err != nil {
		t.Fatal(err)
	}
	proc := exec.Command(os.Args[0], "-test.run=^TestHelperStopTarget$", "--", "--repo", root, "daemon")
	proc.Env = append(os.Environ(), "TRACE_TEST_STOP_TARGET=1")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = proc.Wait(); close(exited) }()
	t.Cleanup(func() { _ = proc.Process.Kill(); <-exited })
	pid := proc.Process.Pid
	for i := 0; i < 200 && !daemon.ProcessIsTraceDaemon(pid, root); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	pidFile := filepath.Join(root, ".trace", "daemon.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	// PATH has git, which locating the repository needs, but no ps.
	bin := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	repo, jsonOutput := root, true
	cmd := daemonCommand(&repo, &jsonOutput)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"stop"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"stopped":true`) {
		t.Fatalf("stop output = %q", out.String())
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon was not signalled")
	}
}
