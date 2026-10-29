package claudecode

import (
	"fmt"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
)

var events = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "StopFailure", "SessionEnd"}

func settingsPath(root string) string { return filepath.Join(root, ".claude", "settings.local.json") }

// Install merges into the original JSON object, including fields added by
// Claude or by other hook providers that Trace does not itself understand.
func Install(root string) error {
	if err := hookconfig.InstallWithMatchers(root, "claude-code", ".claude/settings.local.json", events, map[string]string{"PostToolUse": "Edit|Write|MultiEdit|NotebookEdit|Bash"}); err != nil {
		return fmt.Errorf("installing Claude hooks: %w", err)
	}
	return nil
}

// Uninstall removes only Trace commands from the current settings. Restoring
// an old backup could discard settings changed since a previous installation.
func Uninstall(root string) error {
	if err := hookconfig.Uninstall(root, "claude-code", ".claude/settings.local.json", true); err != nil {
		return fmt.Errorf("removing Claude hooks: %w", err)
	}
	return nil
}
