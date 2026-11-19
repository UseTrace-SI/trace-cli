package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func TestCopyDirSkipsLiveDatabaseFiles(t *testing.T) {
	for _, name := range []string{"trace.db", "trace.db-wal", "trace.db-shm", "trace.db-journal"} {
		t.Run(name, func(t *testing.T) {
			src, dst := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("kept"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(src, name), []byte("live data"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := copyDir(src, dst, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dst, "keep.txt")); err != nil {
				t.Fatalf("regular file was not copied: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dst, name)); !os.IsNotExist(err) {
				t.Fatalf("live database file %s was copied: %v", name, err)
			}
		})
	}
}

func TestCheckpointExcludesLiveDatabaseAndSerializes(t *testing.T) {
	root := t.TempDir()
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "config", "user.email", "test@example.com")
	gitIn(t, root, "config", "user.name", "Test")
	traceDir := filepath.Join(root, ".trace")
	if err := os.MkdirAll(traceDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(traceDir, "keep.txt"), []byte("history"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"trace.db", "trace.db-wal", "trace.db-shm"} {
		if err := os.WriteFile(filepath.Join(traceDir, name), []byte("live"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnableGitNative(root); err != nil {
		t.Fatal(err)
	}
	if err := Checkpoint(root); err != nil {
		t.Fatal(err)
	}
	files := gitIn(t, root, "ls-tree", "-r", "--name-only", CheckpointBranch)
	for _, name := range strings.Split(files, "\n") {
		if strings.HasPrefix(name, "trace.db") {
			t.Fatalf("checkpoint contains a live database file: %s", files)
		}
	}
	// copyDir stores the contents of .trace at the branch root.
	if !strings.Contains(files, "\nkeep.txt") && files != "keep.txt" {
		t.Fatalf("checkpoint is missing trace state: %s", files)
	}
	if _, err := os.Stat(filepath.Join(traceDir, "checkpoint.lock")); err != nil {
		t.Fatalf("checkpoint lock was not created: %v", err)
	}
	// A second checkpoint (which reuses the same lock) must also succeed.
	if err := Checkpoint(root); err != nil {
		t.Fatalf("second checkpoint: %v", err)
	}
}

// Checkpoint reads .trace files with os.ReadFile, which follows symlinks. A
// symlink placed under .trace must never copy outside content into the hidden
// checkpoint branch (which is visible in git history).
func TestCopyDirSkipsSymlinks(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	outsideDir := t.TempDir()
	secret := "PRIVATE-KEY-DO-NOT-COMMIT"
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outsideDir, "secret.txt"), filepath.Join(src, "linked.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(src, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(src, dst, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "keep.txt"))
	if err != nil || string(data) != "kept" {
		t.Fatalf("regular file was not copied: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "linked.txt")); err == nil {
		t.Fatalf("symlink target was copied into checkpoint: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dst, "linked-dir", "secret.txt")); err == nil {
		t.Fatal("symlinked directory target was copied into checkpoint")
	}
}
