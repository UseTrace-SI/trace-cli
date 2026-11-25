package adapters

import (
	"encoding/json"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/kilo"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

// kiloAdapter: Trace's Kilo plugins send generic payloads.
type kiloAdapter struct {
	GenericHooks
	Global
}

func (kiloAdapter) Name() string { return "kilo" }

func (kiloAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Kilo", Verification: Preview}
}

func (kiloAdapter) HookAliases() []string { return []string{"kilo-global"} }

// ResolveHookRoot: the global plugin reports the session's cwd and whether
// Kilo also loaded the project's own Trace plugin, which then reports it.
func (kiloAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	if !c.Global {
		return "", nil
	}
	b, err := c.Body()
	if err != nil {
		return "", err
	}
	var payload struct {
		CWD           string `json:"cwd"`
		ProjectLoaded bool   `json:"project_loaded"`
	}
	if json.Unmarshal(b, &payload) != nil || !filepath.IsAbs(payload.CWD) {
		return "", errNoRoot
	}
	root, err := repo.FindWorktreeRoot(payload.CWD)
	if err != nil {
		return "", err
	}
	if payload.ProjectLoaded {
		return "", errElsewhere
	}
	return root, nil
}

func (kiloAdapter) InstallProject(root string) error { return kilo.Install(root) }
func (kiloAdapter) RemoveProject(root string) error  { return kilo.Uninstall(root) }

func (kiloAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"kilo"}, Profiles: []string{".config/kilo"}}
}

func (kiloAdapter) SetupGlobal(home string, remove bool) error { return kilo.SetupGlobal(home, remove) }

const kiloRestart = "Restart Kilo and confirm the Trace plugin loaded."

func (kiloAdapter) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return kiloRestart, nil
}

func (kiloAdapter) SetupHeadline() (string, bool) { return "Kilo observer installed.", false }
func (kiloAdapter) SetupNotice(string) string     { return kiloRestart }
