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

// Goose exposes the directory of the plugin that launched a hook. Its process
// cwd can belong to a different session, particularly in the desktop server.
func gooseProjectRoot(pluginRoot string) (string, error) {
	if !filepath.IsAbs(pluginRoot) {
		return "", fmt.Errorf("goose plugin root must be absolute")
	}
	pluginRoot = filepath.Clean(pluginRoot)
	if filepath.Base(pluginRoot) != "trace" || filepath.Base(filepath.Dir(pluginRoot)) != "plugins" || filepath.Base(filepath.Dir(filepath.Dir(pluginRoot))) != ".agents" {
		return "", fmt.Errorf("not a Trace project plugin")
	}
	project := filepath.Dir(filepath.Dir(filepath.Dir(pluginRoot)))
	root, err := repo.FindWorktreeRoot(project)
	if err != nil {
		return "", err
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil || project != root {
		return "", fmt.Errorf("goose plugin is not at the worktree root")
	}
	return root, nil
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
