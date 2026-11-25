package adapters

import (
	"github.com/UseTrace-SI/trace-cli/internal/adapters/pi"
)

// piFamily is shared by Pi and Oh My Pi, whose Trace extensions send
// generic payloads.
type piFamily struct {
	GenericHooks
	Global
	name string
}

func (a piFamily) Name() string { return a.name }

func (a piFamily) HookAliases() []string { return []string{a.name + "-global"} }

// ResolveHookRoot: the project extension runs in the repository; the
// global extension reports the session's cwd.
func (piFamily) ResolveHookRoot(c *HookCall) (string, error) {
	if !c.Global {
		return "", nil
	}
	_, root, err := payloadCWD(c)
	return root, err
}

func (a piFamily) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{a.name}, Profiles: []string{"." + a.name}}
}

func (a piFamily) SetupGlobal(home string, remove bool) error {
	return pi.SetupGlobal(home, a.name, remove)
}

func (a piFamily) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return a.SetupNotice(""), nil
}

func (a piFamily) SetupHeadline() (string, bool) { return a.name + " observer installed.", false }

func (a piFamily) SetupNotice(string) string {
	return "Restart " + a.name + " and confirm the Trace extension loaded."
}

func (a piFamily) InstallEvidence() []InstallEvidence {
	return []InstallEvidence{{Path: "." + a.name + "/extensions/trace/index.ts", Contains: "Trace coding-agent extension"}}
}

type piAdapter struct{ piFamily }

func newPi() piAdapter { return piAdapter{piFamily{name: "pi"}} }

func (piAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Pi", Verification: Preview}
}

func (piAdapter) InstallProject(root string) error { return pi.Install(root) }
func (piAdapter) RemoveProject(root string) error  { return pi.Uninstall(root) }
