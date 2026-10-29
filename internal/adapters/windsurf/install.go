package windsurf

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
)

var events = []string{"pre_user_prompt", "post_read_code", "post_write_code", "post_run_command", "post_cascade_response"}

const currentPath = ".devin/hooks.json"
const legacyPath = ".windsurf/hooks.json"

var globalPaths = []string{".codeium/windsurf/hooks.json", ".codeium/hooks.json"}

func configPath(root string) string {
	if data, err := os.ReadFile(filepath.Join(root, currentPath)); err == nil {
		var config struct {
			Hooks map[string]json.RawMessage `json:"hooks"`
		}
		if json.Unmarshal(data, &config) != nil || len(config.Hooks) > 0 {
			return currentPath
		}
		if _, err := os.Stat(filepath.Join(root, legacyPath)); err == nil {
			return legacyPath
		}
		return currentPath
	}
	if _, err := os.Stat(filepath.Join(root, legacyPath)); err == nil {
		return legacyPath
	}
	return currentPath
}

func Install(root string) error {
	return hookconfig.Install(root, "windsurf", configPath(root), events, false)
}

func Uninstall(root string) error {
	for _, path := range []string{currentPath, legacyPath} {
		if err := hookconfig.Uninstall(root, "windsurf", path, false); err != nil {
			return err
		}
	}
	return nil
}

// InstallGlobal installs in both documented user-level locations. Validate
// both files before modifying either, so malformed user config is untouched.
func InstallGlobal(home string) error {
	for _, path := range globalPaths {
		if err := validateGlobal(home, path); err != nil {
			return err
		}
	}
	for _, path := range globalPaths {
		if err := hookconfig.Install(home, "windsurf-global", path, events, false); err != nil {
			return err
		}
	}
	return nil
}

func validateGlobal(home, path string) error {
	b, err := os.ReadFile(filepath.Join(home, path))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading Windsurf hooks: %w", err)
	}
	if len(b) == 0 {
		return nil
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(b, &cfg); err != nil || cfg == nil {
		return fmt.Errorf("invalid Windsurf hook settings at %s", path)
	}
	raw, ok := cfg["hooks"]
	if !ok {
		return nil
	}
	var hooks map[string]json.RawMessage
	if err := json.Unmarshal(raw, &hooks); err != nil || hooks == nil {
		return fmt.Errorf("invalid Windsurf hooks at %s", path)
	}
	for _, name := range events {
		if raw, ok := hooks[name]; ok {
			var entries []json.RawMessage
			if err := json.Unmarshal(raw, &entries); err != nil || entries == nil {
				return fmt.Errorf("invalid Windsurf %s hooks at %s", name, path)
			}
		}
	}
	return nil
}

func UninstallGlobal(home string) error {
	for _, path := range globalPaths {
		if err := hookconfig.Uninstall(home, "windsurf-global", path, false); err != nil {
			return err
		}
	}
	return nil
}

func HasProjectHook(root, name string) bool {
	b, err := os.ReadFile(filepath.Join(root, configPath(root)))
	if err != nil {
		return false
	}
	var cfg struct {
		Hooks map[string][]struct {
			Command          string `json:"command"`
			WorkingDirectory string `json:"working_directory"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return false
	}
	for _, hook := range cfg.Hooks[name] {
		if hook.WorkingDirectory == "" && hookcmd.IsTraceHook(hook.Command, "windsurf") && hookcmd.IsTraceHookFor(hook.Command, "windsurf", name) {
			return true
		}
	}
	return false
}
