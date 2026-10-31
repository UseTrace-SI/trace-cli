package gitx

import (
	"fmt"
	"os/exec"
	"strings"
)

// CreateShadow creates a short-lived shadow branch from the current HEAD.
func CreateShadow(root, name string) error {
	branch := "trace/shadow/" + name
	if out, err := exec.Command("git", "-C", root, "branch", branch, "HEAD").CombinedOutput(); err != nil {
		return fmt.Errorf("creating shadow branch: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// RestoreShadow checks out the shadow branch into a new worktree for rewind.
func RestoreShadow(root, name, dest string) error {
	branch := "trace/shadow/" + name
	if out, err := exec.Command("git", "-C", root, "worktree", "add", dest, branch).CombinedOutput(); err != nil {
		return fmt.Errorf("restoring shadow: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// DeleteShadow removes a shadow branch.
func DeleteShadow(root, name string) error {
	branch := "trace/shadow/" + name
	if out, err := exec.Command("git", "-C", root, "branch", "-D", branch).CombinedOutput(); err != nil {
		return fmt.Errorf("deleting shadow: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// ListShadows returns shadow branch names.
func ListShadows(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "branch", "--list", "trace/shadow/*").Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "* ")
		if line == "" {
			continue
		}
		names = append(names, strings.TrimPrefix(line, "trace/shadow/"))
	}
	return names, nil
}