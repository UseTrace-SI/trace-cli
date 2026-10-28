package cursor

import "github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"

var events = []string{"sessionStart", "beforeSubmitPrompt", "beforeReadFile", "afterFileEdit", "afterShellExecution", "afterAgentThought", "afterAgentResponse", "stop", "sessionEnd"}

func Install(root string) error {
	return hookconfig.Install(root, "cursor", ".cursor/hooks.json", events, false)
}
func Uninstall(root string) error {
	return hookconfig.Uninstall(root, "cursor", ".cursor/hooks.json", false)
}
