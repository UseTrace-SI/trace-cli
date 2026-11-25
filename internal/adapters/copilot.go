package adapters

import (
	"github.com/UseTrace-SI/trace-cli/internal/adapters/copilot"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type copilotAdapter struct {
	Hooks
	Global
}

func (copilotAdapter) Name() string { return "copilot" }

func (copilotAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Copilot CLI", Verification: Preview}
}

func (copilotAdapter) HookAliases() []string { return []string{"copilot-global"} }

// ResolveHookRoot: project hooks run in the repository; a global hook
// routes by the payload's cwd and yields to a project hook there.
func (copilotAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	if !c.Global {
		return "", nil
	}
	_, root, err := payloadCWD(c)
	if err != nil {
		return "", err
	}
	if copilot.HasProjectHook(root, c.Event) {
		return "", errElsewhere
	}
	return root, nil
}

func (copilotAdapter) StartsDaemon(hookEvent string) bool { return hookEvent == "userPromptSubmitted" }

func (copilotAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return copilot.Parse(c.Event, body, root)
}

func (copilotAdapter) InstallProject(root string) error { return copilot.Install(root) }
func (copilotAdapter) RemoveProject(root string) error  { return copilot.Uninstall(root) }

func (copilotAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"copilot"}, Profiles: []string{".copilot"}}
}

func (copilotAdapter) SetupGlobal(home string, remove bool) error {
	if remove {
		return copilot.UninstallGlobal(home)
	}
	return copilot.InstallGlobal(home)
}

const copilotRestart = "Restart Copilot CLI and confirm hooks are active."

func (copilotAdapter) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return copilotRestart, nil
}

func (copilotAdapter) SetupHeadline() (string, bool) { return "Copilot CLI hooks installed.", false }
func (copilotAdapter) SetupNotice(string) string     { return copilotRestart }

func (copilotAdapter) InstallEvidence() []InstallEvidence {
	return []InstallEvidence{{Path: ".github/hooks/usetrace.json", Contains: " hook copilot"}}
}
