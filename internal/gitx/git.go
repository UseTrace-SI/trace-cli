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
