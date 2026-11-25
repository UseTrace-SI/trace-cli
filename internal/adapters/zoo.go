package adapters

import "github.com/UseTrace-SI/trace-cli/internal/adapters/zoo"

// zooAdapter: the Zoo Code observer extension sends generic payloads with
// the session's cwd.
type zooAdapter struct {
	GenericHooks
	Global
}

func (zooAdapter) Name() string { return "zoo" }

func (zooAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Zoo Code", Verification: Preview}
}

func (zooAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	_, root, err := payloadCWD(c)
	return root, err
}

func (zooAdapter) Integration() Integration {
	return Integration{Kind: "Desktop", Profiles: []string{".vscode/extensions/zoocodeorganization.zoo-code-*", ".vscode-insiders/extensions/zoocodeorganization.zoo-code-*"}}
}

func (zooAdapter) SetupGlobal(home string, remove bool) error { return zoo.Setup(home, remove) }

const zooUninstall = "Uninstall the Trace Zoo Code Observer extension in your editor."

// SetupJSONNotice: the extension package must be installed or removed in
// the editor by hand, so setup never activates Zoo Code itself.
func (zooAdapter) SetupJSONNotice(home string, remove bool) (string, map[string]any) {
	if remove {
		return zooUninstall, nil
	}
	return "Install the generated VSIX in your editor and reload the window.", map[string]any{"extension_package": zoo.Path(home)}
}

func (zooAdapter) SetupHeadline() (string, bool) { return "Zoo Code observer package created.", false }

func (zooAdapter) SetupNotice(home string) string {
	return "Install " + zoo.Path(home) + " as a VSIX in your editor and reload the window."
}

func (zooAdapter) SetupRemovedNotice() string { return zooUninstall }
