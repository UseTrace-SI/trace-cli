package gemini

import "github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"

var events = []string{"SessionStart", "BeforeAgent", "AfterTool", "AfterAgent", "SessionEnd"}

func Install(root string) error {
	return hookconfig.Install(root, "gemini", ".gemini/settings.json", events, true)
}
func Uninstall(root string) error {
	return hookconfig.Uninstall(root, "gemini", ".gemini/settings.json", true)
}
