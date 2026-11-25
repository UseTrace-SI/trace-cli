package adapters

import (
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hermes"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// hermesAdapter: the observer plugin runs each hook in the TRACE_HERMES_REPO
// directory, so callbacks use the working directory's repository.
type hermesAdapter struct {
	Hooks
	Global
}

func (hermesAdapter) Name() string { return "hermes" }

func (hermesAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Hermes", Verification: Preview}
}

func (hermesAdapter) StartsDaemon(hookEvent string) bool { return hookEvent == "pre_llm_call" }

func (hermesAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return hermes.Parse(c.Event, body, root)
}

func (hermesAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"hermes"}, Profiles: []string{".hermes"}}
}

func (hermesAdapter) SetupGlobal(home string, remove bool) error { return hermes.Setup(home, remove) }

func (hermesAdapter) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return "Enable trace-observer in Hermes and set TRACE_HERMES_REPO.", nil
}

func (hermesAdapter) SetupHeadline() (string, bool) { return "Hermes observer installed.", false }

func (hermesAdapter) SetupNotice(string) string {
	return "Enable trace-observer in Hermes and set TRACE_HERMES_REPO to the repository path."
}
