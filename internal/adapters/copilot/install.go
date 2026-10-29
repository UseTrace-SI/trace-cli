package copilot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
)

const configPath = ".github/hooks/usetrace.json"
const legacyPath = ".github/copilot/settings.local.json"

var events = []string{"sessionStart", "userPromptSubmitted", "postToolUse", "agentStop", "sessionEnd"}

func Install(root string) error {
	return hookconfig.Install(root, "copilot", configPath, events, false)
}

// HasProjectHook only recognizes an unchanged Trace hook with no additional
// conditions or platform-specific command overrides.
func HasProjectHook(root, name string) bool {
	b, err := os.ReadFile(filepath.Join(root, configPath))
	if err != nil {
		return false
	}
	var cfg struct {
		Version int                        `json:"version"`
		Hooks   map[string]json.RawMessage `json:"hooks"`
	}
	if json.Unmarshal(b, &cfg) != nil || cfg.Version != 1 {
		return false
	}
	var entries []map[string]any
	if json.Unmarshal(cfg.Hooks[name], &entries) != nil {
		return false
	}
	for _, entry := range entries {
		command, ok := entry["command"].(string)
		if len(entry) == 2 && entry["type"] == "command" && ok && hookcmd.IsTraceHook(command, "copilot") && hookcmd.IsTraceHookFor(command, "copilot", name) {
			return true
		}
	}
	return false
}

func Uninstall(root string) error {
	for _, path := range []string{configPath, legacyPath} {
		if err := hookconfig.Uninstall(root, "copilot", path, false); err != nil {
			return err
		}
	}
	return nil
}

func globalRoot(home string) (string, error) {
	config := os.Getenv("COPILOT_HOME")
	if config == "" {
		return filepath.Join(home, ".copilot"), nil
	}
	if !filepath.IsAbs(config) {
		return "", fmt.Errorf("COPILOT_HOME must be absolute; relative paths depend on the Copilot launch directory")
	}
	return filepath.Clean(config), nil
}

func InstallGlobal(home string) error {
	root, err := globalRoot(home)
	if err != nil {
		return err
	}
	return hookconfig.Install(root, "copilot-global", "hooks/usetrace.json", events, false)
}

func UninstallGlobal(home string) error {
	root, err := globalRoot(home)
	if err != nil {
		return err
	}
	return hookconfig.Uninstall(root, "copilot-global", "hooks/usetrace.json", false)
}
