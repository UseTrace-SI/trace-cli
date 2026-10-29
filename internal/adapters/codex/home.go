package codex

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfigHome resolves the profile used by Codex. An explicitly selected home
// must already exist, matching Codex's own configuration requirement.
func ConfigHome(home string) (string, error) {
	configured := os.Getenv("CODEX_HOME")
	if configured == "" {
		return filepath.Join(home, ".codex"), nil
	}
	configured, err := filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("resolving CODEX_HOME: %w", err)
	}
	info, err := os.Stat(configured)
	if err != nil {
		return "", fmt.Errorf("checking CODEX_HOME: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("CODEX_HOME is not a directory")
	}
	return configured, nil
}
