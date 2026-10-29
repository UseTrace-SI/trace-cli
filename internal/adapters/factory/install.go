package factory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
)

var events = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "SubagentStop", "Stop", "Notification", "SessionEnd"}

const hooksPath = ".factory/hooks.json"
const settingsPath = ".factory/settings.json"

func Install(root string) error {
	return install(root, "factory")
}

func InstallGlobal(home string) error {
	return install(home, "factory-global")
}

func install(root, agent string) error {
	if _, err := os.Stat(filepath.Join(root, hooksPath)); err == nil {
		return hookconfig.InstallFlat(root, agent, hooksPath, events)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking Factory hooks: %w", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".factory/hooks/hooks.json")); err == nil {
		return fmt.Errorf("legacy Factory hooks exist; migrate them with Droid's hooks manager before installing Trace")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking legacy Factory hooks: %w", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, settingsPath)); err == nil {
		var config struct {
			Hooks map[string]json.RawMessage `json:"hooks"`
		}
		if json.Unmarshal(data, &config) != nil || len(config.Hooks) > 0 {
			return hookconfig.Install(root, agent, settingsPath, events, true)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading Factory settings: %w", err)
	}
	return hookconfig.InstallFlat(root, agent, hooksPath, events)
}

func Uninstall(root string) error {
	if err := hookconfig.UninstallFlat(root, "factory", hooksPath); err != nil {
		return err
	}
	return hookconfig.Uninstall(root, "factory", settingsPath, true)
}

func UninstallGlobal(home string) error {
	if err := hookconfig.UninstallFlat(home, "factory-global", hooksPath); err != nil {
		return err
	}
	return hookconfig.Uninstall(home, "factory-global", settingsPath, true)
}

// HasProjectHook identifies an unconditional Trace hook in the effective project config.
func HasProjectHook(root, name string) bool {
	b, err := os.ReadFile(filepath.Join(root, hooksPath))
	flat := err == nil
	if os.IsNotExist(err) {
		b, err = os.ReadFile(filepath.Join(root, settingsPath))
	}
	if err != nil {
		return false
	}
	type group struct {
		Matcher      string `json:"matcher"`
		CommandRegex string `json:"commandRegex"`
		Hooks        []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	var hooks map[string][]group
	if flat {
		err = json.Unmarshal(b, &hooks)
	} else {
		var cfg struct {
			Hooks map[string][]group `json:"hooks"`
		}
		err = json.Unmarshal(b, &cfg)
		hooks = cfg.Hooks
	}
	if err != nil {
		return false
	}
	for _, g := range hooks[name] {
		if (g.Matcher != "" && g.Matcher != "*") || g.CommandRegex != "" {
			continue
		}
		for _, h := range g.Hooks {
			if hookcmd.IsTraceHook(h.Command, "factory") && hookcmd.IsTraceHookFor(h.Command, "factory", name) {
				return true
			}
		}
	}
	return false
}
