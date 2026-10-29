package qwen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
)

var events = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "StopFailure", "SessionEnd"}

func Install(root string) error {
	return hookconfig.Install(root, "qwen", ".qwen/settings.json", events, true)
}

func Uninstall(root string) error {
	return hookconfig.Uninstall(root, "qwen", ".qwen/settings.json", true)
}

func InstallGlobal(home string) error {
	dir, err := globalDir(home)
	if err != nil {
		return err
	}
	return hookconfig.Install(dir, "qwen-global", "settings.json", events, true)
}

func UninstallGlobal(home string) error {
	dir, err := globalDir(home)
	if err != nil {
		return err
	}
	return hookconfig.Uninstall(dir, "qwen-global", "settings.json", true)
}

func globalDir(home string) (string, error) {
	dir := os.Getenv("QWEN_HOME")
	if dir == "" {
		return filepath.Join(home, ".qwen"), nil
	}
	if dir == "~" {
		dir = home
	} else if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(home, dir[2:])
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("QWEN_HOME must be absolute or use ~/; relative paths depend on the Qwen launch directory")
	}
	return dir, nil
}

func HasProjectHook(root, name string) bool {
	b, err := os.ReadFile(filepath.Join(root, ".qwen", "settings.json"))
	if err != nil {
		return false
	}
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return false
	}
	for _, group := range cfg.Hooks[name] {
		if group.Matcher != "" {
			continue
		}
		for _, hook := range group.Hooks {
			if hookcmd.IsTraceHook(hook.Command, "qwen") && hookcmd.IsTraceHookFor(hook.Command, "qwen", name) {
				return true
			}
		}
	}
	return false
}
