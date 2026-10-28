package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

func rootFromFlag(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return repo.FindRoot(cwd)
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
