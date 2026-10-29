package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
)

var events = []string{"sessionStart", "beforeSubmitPrompt", "beforeReadFile", "afterFileEdit", "afterShellExecution", "afterAgentThought", "afterAgentResponse", "subagentStop", "stop", "sessionEnd"}

func Install(root string) error {
	return hookconfig.Install(root, "cursor", ".cursor/hooks.json", events, false)
}
func Uninstall(root string) error {
	return hookconfig.Uninstall(root, "cursor", ".cursor/hooks.json", false)
}

func InstallGlobal(home string) error {
	return hookconfig.Install(home, "cursor-global", ".cursor/hooks.json", events, false)
}

func UninstallGlobal(home string) error {
	return hookconfig.Uninstall(home, "cursor-global", ".cursor/hooks.json", false)
}

// HasProjectHook lets the global observer avoid duplicating a project hook.
// Cursor merges both sources, so only skip an event when its matching Trace
// command is present in this workspace's project configuration.
func HasProjectHook(workspace, name string) bool {
	b, err := os.ReadFile(filepath.Join(workspace, ".cursor", "hooks.json"))
	if err != nil {
		return false
	}
	var config struct {
		Hooks map[string][]struct {
			Command string `json:"command"`
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &config) != nil {
		return false
	}
	for _, hook := range config.Hooks[name] {
		if hook.Matcher == "" && hookcmd.IsTraceHook(hook.Command, "cursor") && hookcmd.IsTraceHookFor(hook.Command, "cursor", name) {
			return true
		}
	}
	return false
}
