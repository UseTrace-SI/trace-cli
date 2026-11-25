package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

func rootFromFlag(flag string) (string, error) {
	if flag != "" {
		return repo.FindRoot(flag)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return repo.FindRoot(cwd)
}

// Hooks receive the absolute repository root from their installed command.
// Avoid a git subprocess on every agent event.
func hookRootFromFlag(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	return rootFromFlag("")
}

func initializedRoot(flag string) (string, error) {
	root, err := rootFromFlag(flag)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(root, ".trace", "trace.db")); err != nil {
		return "", fmt.Errorf("trace is not initialized: %w", err)
	}
	return root, nil
}
