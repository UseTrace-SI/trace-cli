package repo

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func FindRoot(cwd string) (string, error) {
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// FindWorktreeRoot locates a normal Git worktree without launching Git. It is
// intended for latency-sensitive hooks and deliberately does not infer roots
// from process-wide GIT_DIR/GIT_WORK_TREE overrides.
func FindWorktreeRoot(directory string) (string, error) {
	if !filepath.IsAbs(directory) {
		return "", fmt.Errorf("worktree path must be absolute")
	}
	current, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", fmt.Errorf("resolving worktree path: %w", err)
	}
	info, err := os.Stat(current)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("worktree path is not a directory")
	}
	for {
		marker := filepath.Join(current, ".git")
		info, err := os.Lstat(marker)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				info, err = os.Stat(marker)
				if err != nil {
					return "", fmt.Errorf("checking Git worktree marker: %w", err)
				}
			}
			gitdir := marker
			if !info.IsDir() {
				if !info.Mode().IsRegular() {
					return "", fmt.Errorf("invalid Git worktree marker")
				}
				f, err := os.Open(marker)
				if err != nil {
					return "", fmt.Errorf("opening Git worktree marker: %w", err)
				}
				data, readErr := io.ReadAll(io.LimitReader(f, 4097))
				_ = f.Close()
				line := strings.TrimSpace(string(data))
				if readErr != nil || len(data) > 4096 || !strings.HasPrefix(line, "gitdir: ") {
					return "", fmt.Errorf("invalid Git worktree marker")
				}
				gitdir = strings.TrimSpace(strings.TrimPrefix(line, "gitdir: "))
				if gitdir == "" {
					return "", fmt.Errorf("invalid Git worktree marker")
				}
				if !filepath.IsAbs(gitdir) {
					gitdir = filepath.Join(current, gitdir)
				}
			}
			if head, err := os.Stat(filepath.Join(gitdir, "HEAD")); err != nil || !head.Mode().IsRegular() {
				return "", fmt.Errorf("invalid Git worktree directory")
			}
			return current, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("checking Git worktree marker: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("not inside a Git worktree")
		}
		current = parent
	}
}

func TraceDir(root string) string { return filepath.Join(root, ".trace") }

// RelativePath resolves path aliases before checking that an event path belongs
// to root. Missing files are supported so delete events can still be recorded.
func RelativePath(root, path string) (string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving repository root: %w", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", fmt.Errorf("resolving repository root: %w", err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving event path: %w", err)
	}
	canonicalPath, err := resolveExistingParent(path)
	if err != nil {
		return "", fmt.Errorf("resolving event path: %w", err)
	}
	rel, err := filepath.Rel(canonicalRoot, canonicalPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("event path is outside the repository")
	}
	return filepath.ToSlash(rel), nil
}

func resolveExistingParent(path string) (string, error) {
	parent := path
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if info, statErr := os.Lstat(parent); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("dangling symlink in event path")
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		missing = append(missing, filepath.Base(parent))
		parent = next
	}
}
