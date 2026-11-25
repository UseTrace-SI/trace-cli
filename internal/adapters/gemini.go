package adapters

import (
	"github.com/UseTrace-SI/trace-cli/internal/adapters/gemini"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// GeminiEnableNotice is the activation step reported for Gemini CLI.
const GeminiEnableNotice = "Ensure Gemini hooks are enabled; some versions require hooks.enabled=true in settings.json. This can enable other configured hooks."

type geminiAdapter struct {
	Hooks
	Global
}

func (geminiAdapter) Name() string { return "gemini" }

func (geminiAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Gemini CLI", Verification: Preview}
}

func (geminiAdapter) HookAliases() []string { return []string{"gemini-global"} }

// HookResponse: Gemini CLI waits for a JSON reply to every hook.
func (geminiAdapter) HookResponse(string) (string, bool) { return "{}", true }

func (geminiAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	return cwdHookRoot(c, gemini.HasProjectHook)
}

func (geminiAdapter) StartsDaemon(hookEvent string) bool { return hookEvent == "BeforeAgent" }

func (geminiAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return gemini.Parse(c.Event, body, root)
}

func (geminiAdapter) InvalidPayloadMessage() string { return "trace hook: invalid Gemini payload" }

func (geminiAdapter) InstallProject(root string) error { return gemini.Install(root) }
func (geminiAdapter) RemoveProject(root string) error  { return gemini.Uninstall(root) }

func (geminiAdapter) ProjectNotice() (string, string) { return "Gemini", GeminiEnableNotice }

func (geminiAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"gemini"}, Profiles: []string{".gemini"}}
}

func (geminiAdapter) SetupGlobal(home string, remove bool) error {
	if remove {
		return gemini.UninstallGlobal(home)
	}
	return gemini.InstallGlobal(home)
}

func (geminiAdapter) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return GeminiEnableNotice, nil
}

func (geminiAdapter) SetupHeadline() (string, bool) { return "", true }
func (geminiAdapter) SetupNotice(string) string     { return GeminiEnableNotice }
