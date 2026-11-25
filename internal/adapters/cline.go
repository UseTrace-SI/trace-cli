package adapters

import (
	"encoding/json"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/cline"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

type clineAdapter struct {
	Hooks
	Global
}

func (clineAdapter) Name() string { return "cline" }

func (clineAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Cline CLI", Verification: Preview}
}

// ResolveHookRoot routes by the single workspace root. Project and global
// hooks share the name, so a hook other than the repository's own
// unchanged project hook yields to it.
func (clineAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	b, err := c.Body()
	if err != nil {
		return "", err
	}
	var payload struct {
		WorkspaceRoots []string `json:"workspaceRoots"`
	}
	if json.Unmarshal(b, &payload) != nil || len(payload.WorkspaceRoots) != 1 || !filepath.IsAbs(payload.WorkspaceRoots[0]) {
		return "", errNoRoot
	}
	root, err := repo.FindWorktreeRoot(payload.WorkspaceRoots[0])
	if err != nil {
		return "", err
	}
	if cline.HasProjectHook(root, c.Event) {
		return "", errElsewhere
	}
	return root, nil
}

func (clineAdapter) StartsDaemon(hookEvent string) bool {
	return hookEvent == "TaskStart" || hookEvent == "TaskResume"
}

func (clineAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return cline.Parse(c.Event, body, root)
}

func (clineAdapter) InstallProject(root string) error { return cline.Install(root) }
func (clineAdapter) RemoveProject(root string) error  { return cline.Uninstall(root) }

func (clineAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"cline"}, Profiles: []string{".cline"}}
}

func (clineAdapter) SetupGlobal(home string, remove bool) error {
	if remove {
		return cline.UninstallGlobal(home)
	}
	return cline.InstallGlobal(home)
}
