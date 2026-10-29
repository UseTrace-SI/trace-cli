package gitx

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func RevParse(root, ref string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", ref).Output()
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", ref, err)
	}
	return strings.TrimSpace(string(out)), nil
}
func Head(root string) (string, error) { return RevParse(root, "HEAD") }
func WorktreeAdd(root, path, head string) error {
	out, err := exec.Command("git", "-C", root, "worktree", "add", "--detach", path, head).CombinedOutput()
	if err != nil {
		return fmt.Errorf("creating worktree: %s: %w", out, err)
	}
	return nil
}

func WorktreeList(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	return paths, nil
}

func WorktreeRemove(root, path string, force bool) error {
	args := []string{"-C", root, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("removing worktree: %s: %w", out, err)
	}
	return nil
}

func IsClean(root string) bool {
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) == 0
}

// MainRepoRoot returns the top-level of the repository that owns this worktree.
// For a normal clone it is root itself; for a linked worktree it is the main
// checkout that created it.
func MainRepoRoot(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("resolving common git dir: %w", err)
	}
	common := strings.TrimSpace(string(out))
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	common = filepath.Clean(common)
	// common is .../<repo>/.git (or .git/worktrees/<name> for old layouts)
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), nil
	}
	// walk up until we find a .git directory
	dir := common
	for {
		if filepath.Base(dir) == ".git" {
			return filepath.Dir(dir), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return root, nil
		}
		dir = parent
	}
}
func GitPath(root, name string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-path", name).Output()
	if err != nil {
		return "", fmt.Errorf("finding git path: %w", err)
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return p, nil
}
