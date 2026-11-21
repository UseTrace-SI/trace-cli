package daemon

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// A repository can ship .trace/ entries (Git materialises tracked symlinks),
// so a hook or the hook-started daemon must never write, truncate, create or
// delete anything outside the repository through one of them.
func TestDaemonStateDoesNotFollowPlantedSymlinks(t *testing.T) {
	const precious = "line one of an important file\n"
	frame := []byte(`{"kind":"turn_start","at":1}` + "\n")
	nop := func(event.Event) error { return nil }
	send := func(dir string) { _ = Send(dir, event.Event{Kind: event.TurnStart, Agent: "test", At: 1}) }
	for _, tc := range []struct {
		name   string
		link   string // entry planted in .trace; empty plants .trace itself
		target string // "file" (existing), "missing", "dir" or "socket"
		run    func(dir string)
	}{
		{"hook spool append", "spool.jsonl", "file", send},
		{"spool drain", "spool.jsonl", "file", func(dir string) { _ = ingestSpoolChecked(dir, nop) }},
		{"pid file", daemonPIDName, "file", func(dir string) { _ = WriteDaemonIdentity(dir) }},
		{"start time", daemonStartTime, "file", func(dir string) { _ = WriteDaemonIdentity(dir) }},
		{"batch marker", "batch.ready", "file", func(dir string) { markBatchReady(dir)() }},
		{"durable marker", "durable.ready", "file", func(dir string) { markBatchReady(dir)() }},
		{"spool readiness", "spool.ready", "file", func(dir string) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = serveSpoolChecked(ctx, dir, nil, nop)
		}},
		{"daemon lock", daemonLockName, "missing", func(dir string) {
			lock, _, _ := TryAcquireDaemonLock(dir)
			ReleaseDaemonLock(lock)
		}},
		{"overflow lock", spoolOverflowLock, "missing", func(dir string) { _ = appendOverflow(dir, frame) }},
		{"overflow marker", spoolOverflowMarker, "missing", func(dir string) { _ = appendOverflow(dir, frame) }},
		{"overflow queue", "spool-pending", "dir", func(dir string) {
			_ = appendOverflow(dir, frame)
			_ = ingestSpoolChecked(dir, nop)
		}},
		{"quarantine", "quarantine", "dir", func(dir string) { _ = quarantineFrame(dir, []byte("{bad")) }},
		// Another process listening elsewhere must not receive hook events.
		{"daemon socket", "sock", "socket", send},
		{"trace directory", "", "dir", func(dir string) {
			send(dir)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_ = ServeChecked(ctx, dir, nop)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "cloned-repo")
			dir := filepath.Join(root, ".trace")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(parent, "victim")
			// Old queue and quarantine entries would be consumed, renamed or
			// pruned if the daemon treated the victim directory as its own.
			stale := time.Now().Add(-2 * spoolRetention)
			sentinels := map[string]string{
				"00000000000000000001-a.ready": string(frame),
				"old.tmp":                      precious,
				"old.frame":                    precious,
				"old.ready.invalid":            precious,
			}
			var listener net.Listener
			switch tc.target {
			case "socket":
				var err error
				if listener, err = net.Listen("unix", victim); err != nil {
					t.Skipf("cannot listen on %s: %v", victim, err)
				}
				defer listener.Close()
			case "file":
				if err := os.WriteFile(victim, []byte(precious), 0600); err != nil {
					t.Fatal(err)
				}
			case "dir":
				if err := os.Mkdir(victim, 0700); err != nil {
					t.Fatal(err)
				}
				for name, body := range sentinels {
					path := filepath.Join(victim, name)
					if err := os.WriteFile(path, []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, stale, stale); err != nil {
						t.Fatal(err)
					}
				}
			}
			link := filepath.Join(dir, tc.link)
			if tc.link == "" {
				if err := os.Remove(dir); err != nil {
					t.Fatal(err)
				}
				link = dir
			}
			rel, err := filepath.Rel(filepath.Dir(link), victim)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(rel, link); err != nil {
				t.Fatal(err)
			}

			tc.run(dir)

			switch tc.target {
			case "socket":
				_ = listener.(*net.UnixListener).SetDeadline(time.Now().Add(50 * time.Millisecond))
				if conn, err := listener.Accept(); err == nil {
					conn.Close()
					t.Fatalf("hook connected to a socket outside the repository through %s", tc.link)
				}
			case "file":
				if got, err := os.ReadFile(victim); err != nil || string(got) != precious {
					t.Fatalf("file outside the repository was modified through %s: %q, %v", tc.link, got, err)
				}
			case "missing":
				if _, err := os.Lstat(victim); !os.IsNotExist(err) {
					t.Fatalf("file outside the repository was created through %s: %v", tc.link, err)
				}
			case "dir":
				entries, err := os.ReadDir(victim)
				if err != nil {
					t.Fatal(err)
				}
				var names []string
				for _, entry := range entries {
					names = append(names, entry.Name())
				}
				want := make([]string, 0, len(sentinels))
				for name := range sentinels {
					want = append(want, name)
				}
				sort.Strings(want)
				if strings.Join(names, ",") != strings.Join(want, ",") {
					t.Fatalf("directory outside the repository changed through %q: %v, want %v", tc.link, names, want)
				}
				for name, body := range sentinels {
					if got, err := os.ReadFile(filepath.Join(victim, name)); err != nil || string(got) != body {
						t.Fatalf("%s outside the repository changed: %q, %v", name, got, err)
					}
				}
			}
		})
	}
}

// Marker reads sit on the hook path. Reading through a planted symlink would
// let a repository choose their contents, or point them at an endless device
// such as /dev/zero and stall the agent.
func TestDaemonMarkersAreNotReadThroughSymlinks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		links []string
		read  func(dir string) bool
	}{
		{"batch marker", []string{"batch.ready", daemonPIDName}, batchReady},
		{"durable marker", []string{"durable.ready", daemonPIDName}, func(dir string) bool { return markerMatches(dir, "durable.ready") }},
		{"personal sync marker", []string{"personal-sync.ready", daemonPIDName}, PersonalSyncReady},
		{"start time", []string{daemonStartTime}, func(dir string) bool { return readDaemonStartTime(dir) != 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "repo", ".trace")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(parent, "victim"), []byte("4242"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, name := range tc.links {
				if err := os.Symlink("../../victim", filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.read(dir) {
				t.Fatal("daemon state was read through a planted symlink")
			}
		})
	}
}

// The daemon checks once at startup that its state directory is local: a
// real directory that Git does not track. Hooks never run Git themselves.
func TestCheckTraceDirRefusesRepositoryControlledState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		plant  func(t *testing.T, root string)
		refuse bool
	}{
		{"absent", func(*testing.T, string) {}, false},
		{"untracked directory", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, ".trace", "spool.jsonl"), "")
		}, false},
		{"symlinked directory", func(t *testing.T, root string) {
			if err := os.Mkdir(filepath.Join(root, "elsewhere"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("elsewhere", filepath.Join(root, ".trace")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"regular file", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, ".trace"), "")
		}, true},
		{"tracked file", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, ".trace", "config.toml"), "git_native = true\n")
			gitAdd(t, root, ".trace")
		}, true},
		{"tracked symlink", func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../victim", filepath.Join(root, ".trace", daemonPIDName)); err != nil {
				t.Fatal(err)
			}
			gitAdd(t, root, ".trace")
		}, true},
		// On a case-insensitive file system .TRACE/ is the same directory.
		{"tracked case variant", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, ".TRACE", "spool.jsonl"), "")
			gitAdd(t, root, ".TRACE")
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
				t.Fatalf("git init: %s: %v", out, err)
			}
			tc.plant(t, root)
			err := CheckTraceDir(root)
			if tc.refuse && err == nil {
				t.Fatal("repository-controlled .trace was accepted")
			}
			if !tc.refuse && err != nil {
				t.Fatalf("local .trace was refused: %v", err)
			}
		})
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func gitAdd(t *testing.T, root string, paths ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", root, "add", "--"}, paths...)...).CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %v", out, err)
	}
}
