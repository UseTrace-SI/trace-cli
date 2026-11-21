package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// rewindRepo creates a Git repository holding files, opens an engine on it and
// records the baseline as step 0.
func rewindRepo(t *testing.T, files map[string]string) (string, *Engine) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	writeFiles(t, root, files)
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	return root, e
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// describePath summarizes what is on disk at p, without following symlinks.
func describePath(t *testing.T, p string) string {
	t.Helper()
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return "missing"
	}
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(p)
		if err != nil {
			t.Fatal(err)
		}
		return "symlink to " + target
	}
	if info.IsDir() {
		return "directory"
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return "file " + string(b)
}

func keyframeCount(t *testing.T, e *Engine) int {
	t.Helper()
	var n int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM keyframes").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The safety keyframe is a fresh scan, which skips ignored files and anything
// that is not a regular file. Replacing such a path would make the rewind
// impossible to undo (the undo step deletes it), so the rewind is refused.
func TestRewindRefusesToReplaceUncapturedPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		// uncapture leaves notes.txt on disk where the next scan skips it.
		uncapture func(t *testing.T, root string)
	}{
		{".traceignore", func(t *testing.T, root string) {
			writeFiles(t, root, map[string]string{".traceignore": "notes.txt\n", "notes.txt": "hours of new work\n"})
		}},
		{".gitignore", func(t *testing.T, root string) {
			writeFiles(t, root, map[string]string{".gitignore": "notes.txt\n", "notes.txt": "hours of new work\n"})
		}},
		{"symlink", func(t *testing.T, root string) {
			notes := filepath.Join(root, "notes.txt")
			if err := os.Remove(notes); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("shared/notes.txt", notes); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, e := rewindRepo(t, map[string]string{"notes.txt": "old draft\n", "other.txt": "old other\n"})
			tc.uncapture(t, root)
			writeFiles(t, root, map[string]string{"other.txt": "new other\n"})
			notes := filepath.Join(root, "notes.txt")
			before := describePath(t, notes)
			keyframes := keyframeCount(t, e)
			for _, dryRun := range []bool{true, false} {
				if _, _, err := e.Rewind(0, "", dryRun); err == nil || !strings.Contains(err.Error(), "notes.txt") {
					t.Fatalf("rewind (dry run %t) did not refuse to replace uncaptured notes.txt: %v", dryRun, err)
				}
			}
			if after := describePath(t, notes); after != before {
				t.Fatalf("refused rewind changed notes.txt: %s, want %s", after, before)
			}
			if got := describePath(t, filepath.Join(root, "other.txt")); got != "file new other\n" {
				t.Fatalf("refused rewind changed other.txt: %s", got)
			}
			if n := keyframeCount(t, e); n != keyframes {
				t.Fatalf("refused rewind recorded keyframes: %d, want %d", n, keyframes)
			}
			// Limiting the restore to other paths still works.
			if _, _, err := e.Rewind(0, "other.txt", false); err != nil {
				t.Fatal(err)
			}
			if got := describePath(t, filepath.Join(root, "other.txt")); got != "file old other\n" {
				t.Fatalf("limited rewind did not restore other.txt: %s", got)
			}
			if after := describePath(t, notes); after != before {
				t.Fatalf("limited rewind changed notes.txt: %s, want %s", after, before)
			}
		})
	}
}

// Paths the safety keyframe does contain may still change type or spelling.
func TestRewindReplacesCapturedPaths(t *testing.T) {
	for _, tc := range []struct {
		name   string
		step0  map[string]string
		change func(t *testing.T, root string)
		path   string // checked after the rewind and after its undo
	}{
		{"file to directory", map[string]string{"entry": "old\n"}, func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "entry")); err != nil {
				t.Fatal(err)
			}
			writeFiles(t, root, map[string]string{"entry/child.txt": "new\n"})
		}, "entry"},
		{"directory to file", map[string]string{"entry/child.txt": "old\n"}, func(t *testing.T, root string) {
			if err := os.RemoveAll(filepath.Join(root, "entry")); err != nil {
				t.Fatal(err)
			}
			writeFiles(t, root, map[string]string{"entry": "new\n"})
		}, "entry"},
		// On case-insensitive file systems (the macOS default) the old
		// spelling of a case-only rename resolves to the captured file; an
		// ignored hard link stands in for that here.
		{"case-only rename", map[string]string{"notes.txt": "old\n"}, func(t *testing.T, root string) {
			if err := os.Rename(filepath.Join(root, "notes.txt"), filepath.Join(root, "Notes.txt")); err != nil {
				t.Fatal(err)
			}
			writeFiles(t, root, map[string]string{".gitignore": "/notes.txt\n", "Notes.txt": "renamed\n"})
			if err := os.Link(filepath.Join(root, "Notes.txt"), filepath.Join(root, "notes.txt")); err != nil {
				t.Skipf("hard links unavailable: %v", err)
			}
		}, "Notes.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, e := rewindRepo(t, tc.step0)
			tc.change(t, root)
			p := filepath.Join(root, tc.path)
			before := describePath(t, p)
			safety, _, err := e.Rewind(0, "", false)
			if err != nil {
				t.Fatal(err)
			}
			if got := describePath(t, p); got == before {
				t.Fatalf("rewind left %s unchanged: %s", tc.path, got)
			}
			if _, _, err := e.Rewind(safety, "", false); err != nil {
				t.Fatal(err)
			}
			if got := describePath(t, p); got != before {
				t.Fatalf("undo restored %s as %s, want %s", tc.path, got, before)
			}
		})
	}
}
