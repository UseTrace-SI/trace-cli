package adapters

import (
	"github.com/UseTrace-SI/trace-cli/internal/adapters/agy"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type agyAdapter struct{ Hooks }

func (agyAdapter) Name() string { return "agy" }

func (agyAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Antigravity CLI", Verification: Preview}
}

// HookResponse: Antigravity waits for a reply to every hook and an explicit
// decision on Stop.
func (agyAdapter) HookResponse(hookEvent string) (string, bool) {
	if hookEvent == "Stop" {
		return `{"decision":"allow"}`, true
	}
	return "{}", true
}

func (agyAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return agy.Parse(c.Event, body, root)
}

func (agyAdapter) InvalidPayloadMessage() string { return "trace hook: invalid Antigravity payload" }

func (agyAdapter) InstallProject(root string) error { return agy.Install(root) }
func (agyAdapter) RemoveProject(root string) error  { return agy.Uninstall(root) }

func (agyAdapter) LaunchCommand(context string) (string, []string) {
	return "agy", []string{"--prompt-interactive", context}
}
