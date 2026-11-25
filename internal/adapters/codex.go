package adapters

import (
	"github.com/UseTrace-SI/trace-cli/internal/adapters/codexhook"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// codexAdapter receives Codex hooks; per repository, the daemon reads
// Codex rollouts when the codex.enabled marker exists.
type codexAdapter struct {
	Hooks
	Global
}

func (codexAdapter) Name() string { return "codex" }

func (codexAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Codex", Verification: Verified}
}

// HookResponse: Codex waits for a JSON reply to Stop.
func (codexAdapter) HookResponse(hookEvent string) (string, bool) {
	if hookEvent == "Stop" {
		return "{}", true
	}
	return "", false
}

func (codexAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	_, root, err := payloadCWD(c)
	return root, err
}

func (codexAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return codexhook.Parse(c.Event, body, root)
}

func (codexAdapter) EnableMarker() Marker {
	return Marker{File: "codex.enabled", Label: "Codex", RemovedByUninstall: true}
}

func (codexAdapter) Integration() Integration {
	return Integration{Kind: "CLI/Desktop", Commands: []string{"codex"}, Profiles: []string{".codex"}, Apps: []string{"Codex.app"}}
}

func (codexAdapter) SetupNeedsStateDir() bool { return true }

func (codexAdapter) SetupGlobal(home string, remove bool) error {
	if remove {
		return codexhook.Uninstall(home)
	}
	return codexhook.Install(home)
}

func (codexAdapter) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return "Review and trust the Codex hooks with /hooks; ensure hooks are enabled.", nil
}

func (codexAdapter) SetupHeadline() (string, bool) { return "", true }

func (codexAdapter) SetupNotice(string) string {
	return "In Codex, review and trust the new hooks with /hooks."
}

func (codexAdapter) LaunchCommand(context string) (string, []string) {
	return "codex", []string{context}
}

func (codexAdapter) ReviewCommand() (string, []string) {
	return "codex", []string{"exec", "--sandbox", "read-only", "--ephemeral", "--color", "never", "-"}
}
