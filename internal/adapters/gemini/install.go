package gemini

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
)

var events = []string{"SessionStart", "BeforeAgent", "AfterTool", "AfterAgent", "SessionEnd"}

func Install(root string) error {
	return hookconfig.Install(root, "gemini", ".gemini/settings.json", events, true)
}
func Uninstall(root string) error {
	return hookconfig.Uninstall(root, "gemini", ".gemini/settings.json", true)
}

func InstallGlobal(home string) error {
	root, err := globalRoot(home)
	if err != nil {
		return err
	}
	return hookconfig.Install(root, "gemini-global", ".gemini/settings.json", events, true)
}

func UninstallGlobal(home string) error {
	root, err := globalRoot(home)
	if err != nil {
		return err
	}
	return hookconfig.Uninstall(root, "gemini-global", ".gemini/settings.json", true)
}

func globalRoot(home string) (string, error) {
	if override := os.Getenv("GEMINI_CLI_HOME"); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("GEMINI_CLI_HOME must be absolute")
		}
		return override, nil
	}
	return home, nil
}

// HasProjectHook detects an unrestricted managed hook in project settings.
func HasProjectHook(root, name string) bool {
	b, err := os.ReadFile(filepath.Join(root, ".gemini", "settings.json"))
	if err != nil {
		return false
	}
	var cfg struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return false
	}
	var groups []struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(cfg.Hooks[name], &groups) != nil {
		return false
	}
	for _, group := range groups {
		if group.Matcher != "" {
			continue
		}
		for _, hook := range group.Hooks {
			if hookcmd.IsTraceHook(hook.Command, "gemini") && hookcmd.IsTraceHookFor(hook.Command, "gemini", name) {
				return true
			}
		}
	}
	return false
}
