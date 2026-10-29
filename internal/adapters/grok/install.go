package grok

import "github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"

const hooksPath = ".grok/hooks/trace.json"

var events = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"}

func Install(root string) error {
	return hookconfig.Install(root, "grok", hooksPath, events, true)
}

func Uninstall(root string) error {
	return hookconfig.Uninstall(root, "grok", hooksPath, true)
}
