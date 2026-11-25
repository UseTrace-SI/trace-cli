package adapters

import (
	"github.com/UseTrace-SI/trace-cli/internal/adapters/qwen"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// QwenEnableNotice is the activation step reported for Qwen Code.
const QwenEnableNotice = "Use a Qwen Code version that supports hooks and confirm they are enabled; version 0.0.14 does not emit Trace hooks."

type qwenAdapter struct {
	Hooks
	Global
}

func (qwenAdapter) Name() string { return "qwen" }

func (qwenAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Qwen Code", Verification: Preview}
}

func (qwenAdapter) HookAliases() []string { return []string{"qwen-global"} }

// HookResponse: Qwen Code waits for a JSON reply to every hook.
func (qwenAdapter) HookResponse(string) (string, bool) { return "{}", true }

func (qwenAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	return cwdHookRoot(c, qwen.HasProjectHook)
}

func (qwenAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return qwen.Parse(c.Event, body, root)
}

func (qwenAdapter) InvalidPayloadMessage() string { return "trace hook: invalid Qwen payload" }

func (qwenAdapter) InstallProject(root string) error { return qwen.Install(root) }
func (qwenAdapter) RemoveProject(root string) error  { return qwen.Uninstall(root) }

func (qwenAdapter) ProjectNotice() (string, string) { return "Qwen", QwenEnableNotice }

func (qwenAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"qwen"}, Profiles: []string{".qwen"}}
}

func (qwenAdapter) SetupGlobal(home string, remove bool) error {
	if remove {
		return qwen.UninstallGlobal(home)
	}
	return qwen.InstallGlobal(home)
}

func (qwenAdapter) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return QwenEnableNotice, nil
}

func (qwenAdapter) SetupHeadline() (string, bool) { return "", true }
func (qwenAdapter) SetupNotice(string) string     { return QwenEnableNotice }
