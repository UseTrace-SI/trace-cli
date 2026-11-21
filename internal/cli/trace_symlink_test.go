package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A cloned repository can ship .trace/ symlinks such as
// `.trace/daemon.pid -> ../../victim`. Neither the hook nor the daemon that a
// hook starts may write through them. When Git tracks the entries, the daemon
// refuses to start before touching any state; when they are untracked (for
// example extracted from an archive), state files are never followed.
func TestPlantedTraceSymlinksAreNotFollowed(t *testing.T) {
	bin := buildTrace(t)
	const precious = "line one of an important file\n"
	for _, tc := range []struct {
		name    string
		tracked bool
	}{
		{"tracked", true},
		{"untracked", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "cloned-repo")
			if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
				t.Fatalf("git init: %s: %v", out, err)
			}
			traceDir := filepath.Join(root, ".trace")
			if err := os.MkdirAll(traceDir, 0755); err != nil {
				t.Fatal(err)
			}
			victims := map[string]string{"daemon.pid": "victim-pid.txt", "spool.jsonl": "victim-spool.txt"}
			for link, target := range victims {
				if err := os.WriteFile(filepath.Join(parent, target), []byte(precious), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../../"+target, filepath.Join(traceDir, link)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.tracked {
				if out, err := exec.Command("git", "-C", root, "add", ".trace").CombinedOutput(); err != nil {
					t.Fatalf("git add: %s: %v", out, err)
				}
			}
			env := append(os.Environ(), "TRACE_HOME="+t.TempDir(), "HOME="+t.TempDir())

			hook := exec.Command(bin, "hook", "claude-code", "PostToolUse")
			hook.Dir = root
			hook.Env = env
			hook.Stdin = strings.NewReader(fmt.Sprintf(`{"session_id":"s1","cwd":%q,"tool_name":"Edit","tool_input":{"file_path":%q}}`, root, filepath.Join(root, "main.go")))
			var hookOut bytes.Buffer
			hook.Stdout = &hookOut
			if err := hook.Run(); err != nil || hookOut.Len() != 0 {
				t.Fatalf("hook: %v, stdout %q", err, hookOut.String())
			}

			// The same command a hook launches. It must stop by itself; the
			// deadline only bounds a daemon that kept running on old code.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			daemon := exec.CommandContext(ctx, bin, "--repo", root, "daemon")
			daemon.Env = env
			var stderr bytes.Buffer
			daemon.Stderr = &stderr
			err := daemon.Run()
			if ctx.Err() != nil {
				t.Errorf("daemon kept running with planted .trace symlinks")
			} else if err == nil {
				t.Errorf("daemon exited successfully with planted .trace symlinks")
			}
			for _, target := range victims {
				if got, err := os.ReadFile(filepath.Join(parent, target)); err != nil || string(got) != precious {
					t.Errorf("file outside the repository was modified: %s now %q, %v", target, got, err)
				}
			}
			if tc.tracked {
				if !strings.Contains(stderr.String(), "Git tracks") {
					t.Errorf("daemon did not report the tracked .trace: %q", stderr.String())
				}
				if _, err := os.Stat(filepath.Join(traceDir, "trace.db")); !os.IsNotExist(err) {
					t.Errorf("daemon opened history before refusing a tracked .trace: %v", err)
				}
			}
		})
	}
}
