package adapters

import (
	"github.com/UseTrace-SI/trace-cli/internal/adapters/grok"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type grokAdapter struct{ Hooks }

func (grokAdapter) Name() string { return "grok" }

func (grokAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Grok Build", Verification: Preview}
}

func (grokAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return grok.Parse(c.Event, body, root)
}

func (grokAdapter) InstallProject(root string) error { return grok.Install(root) }
func (grokAdapter) RemoveProject(root string) error  { return grok.Uninstall(root) }
