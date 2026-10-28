package repo

import (
	"fmt"
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

func TraceDir(root string) string { return filepath.Join(root, ".trace") }
