package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const CheckpointBranch = "trace/checkpoints/v1"

func GitNativeEnabled(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".trace", "git-native"))
	return err == nil
}

// EnableGitNative creates the opt-in marker.
func EnableGitNative(root string) error {
	dir := filepath.Join(root, ".trace")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "git-native"), []byte("1\n"), 0600)
}

// DisableGitNative removes the opt-in marker.
func DisableGitNative(root string) error {
	err := os.Remove(filepath.Join(root, ".trace", "git-native"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// checkpointLock serializes checkpoints, including ones started by a different
// process. Git worktree creation and the branch checkout are not safe to run
// concurrently against the same repository.
func checkpointLock(root string) (func(), error) {
	dir := filepath.Join(root, ".trace")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating trace directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "checkpoint.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening checkpoint lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("locking checkpoints: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// Checkpoint commits the current .trace state to the hidden branch
// trace/checkpoints/v1 without touching the user's working branch.
func Checkpoint(root string) error {
	if !GitNativeEnabled(root) {
		return nil
	}
	release, err := checkpointLock(root)
	if err != nil {
		return err
	}
	defer release()
	// Ensure branch exists.
	branchExists := exec.Command("git", "-C", root, "rev-parse", "--verify", CheckpointBranch)
	if err := branchExists.Run(); err != nil {
		// Create orphan branch with an empty commit.
		tmp, err := os.MkdirTemp("", "trace-cp-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if out, err := exec.Command("git", "-C", root, "worktree", "add", "--orphan", "-b", CheckpointBranch, tmp).CombinedOutput(); err != nil {
			return fmt.Errorf("creating checkpoint branch: %s: %w", strings.TrimSpace(string(out)), err)
		}
		// empty commit
		if out, err := exec.Command("git", "-C", tmp, "commit", "--allow-empty", "-m", "trace: initial checkpoint").CombinedOutput(); err != nil {
			exec.Command("git", "-C", root, "worktree", "remove", "--force", tmp).Run()
			return fmt.Errorf("initial commit: %s: %w", strings.TrimSpace(string(out)), err)
		}
		exec.Command("git", "-C", root, "worktree", "remove", "--force", tmp).Run()
	}

	tmp, err := os.MkdirTemp("", "trace-cp-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if out, err := exec.Command("git", "-C", root, "worktree", "add", tmp, CheckpointBranch).CombinedOutput(); err != nil {
		return fmt.Errorf("worktree add: %s: %w", strings.TrimSpace(string(out)), err)
	}
	defer exec.Command("git", "-C", root, "worktree", "remove", "--force", tmp).Run()

	// Copy .trace (excluding sockets/pids/companion state) into worktree.
	src := filepath.Join(root, ".trace")
	if err := copyDir(src, tmp, map[string]bool{
		"sock": true, "daemon.pid": true, "daemon.lock": true,
		"brain": true, "graph": true, "companions": true,
	}); err != nil {
		return err
	}
	if out, err := exec.Command("git", "-C", tmp, "add", "-A").CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// Commit if there are changes.
	status, _ := exec.Command("git", "-C", tmp, "status", "--porcelain").Output()
	if len(strings.TrimSpace(string(status))) == 0 {
		return nil
	}
	if out, err := exec.Command("git", "-C", tmp, "commit", "-m", "trace: checkpoint").CombinedOutput(); err != nil {
		return fmt.Errorf("commit: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func copyDir(src, dst string, skip map[string]bool) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		// The live database (and its WAL/SHM sidecars) must never be copied by
		// a plain file read: a concurrent write can make the copy inconsistent.
		if skip[parts[0]] || skip[info.Name()] || strings.HasPrefix(info.Name(), "trace.db") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
