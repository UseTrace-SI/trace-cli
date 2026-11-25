package adapters

import (
	"github.com/UseTrace-SI/trace-cli/internal/adapters/kimi"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type kimiAdapter struct {
	Hooks
	Global
}

func (kimiAdapter) Name() string { return "kimi" }

func (kimiAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Kimi Code CLI", Verification: Preview}
}

func (kimiAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	_, root, err := payloadCWD(c)
	return root, err
}

func (kimiAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return kimi.Parse(c.Event, body, root)
}

func (kimiAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"kimi"}, Profiles: []string{".kimi-code"}}
}

func (kimiAdapter) SetupGlobal(home string, remove bool) error {
	if remove {
		return kimi.Uninstall(home)
	}
	return kimi.Install(home)
}
