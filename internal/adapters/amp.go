package adapters

import "github.com/UseTrace-SI/trace-cli/internal/adapters/amp"

// ampAdapter: Trace's Amp plugins send generic payloads.
type ampAdapter struct {
	GenericHooks
	Global
}

func (ampAdapter) Name() string { return "amp" }

func (ampAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Amp", Verification: Preview}
}

func (ampAdapter) HookAliases() []string { return []string{"amp-global"} }

// ResolveHookRoot: the project plugin runs in the repository; the global
// plugin reports the session's cwd.
func (ampAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	if !c.Global {
		return "", nil
	}
	_, root, err := payloadCWD(c)
	return root, err
}

func (ampAdapter) InstallProject(root string) error { return amp.Install(root) }
func (ampAdapter) RemoveProject(root string) error  { return amp.Uninstall(root) }

func (ampAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"amp"}, Profiles: []string{".config/amp"}}
}

func (ampAdapter) SetupGlobal(home string, remove bool) error { return amp.SetupGlobal(home, remove) }

const ampReload = "Reload Amp plugins and check activation with amp plugins list."

func (ampAdapter) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return ampReload, nil
}

func (ampAdapter) SetupHeadline() (string, bool) { return "Amp observer installed.", false }
func (ampAdapter) SetupNotice(string) string     { return ampReload }
