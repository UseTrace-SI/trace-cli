package adapters

import (
	"encoding/json"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/cursor"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

type cursorAdapter struct {
	Hooks
	Global
}

func (cursorAdapter) Name() string { return "cursor" }

func (cursorAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Cursor Agent", Verification: Preview}
}

func (cursorAdapter) HookAliases() []string { return []string{"cursor-global"} }

// HookResponse: Cursor waits for a reply to every hook and needs explicit
// permission for prompts and reads.
func (cursorAdapter) HookResponse(hookEvent string) (string, bool) {
	switch hookEvent {
	case "beforeSubmitPrompt":
		return `{"continue":true}`, true
	case "beforeReadFile":
		return `{"permission":"allow"}`, true
	}
	return "{}", true
}

// ResolveHookRoot routes by the single workspace folder. Multiple folders
// do not identify which repository owns a prompt; do not attach the turn
// to an arbitrary project.
func (cursorAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	b, err := c.Body()
	if err != nil {
		return "", err
	}
	var payload struct {
		WorkspaceRoots []string `json:"workspace_roots"`
	}
	if json.Unmarshal(b, &payload) != nil || len(payload.WorkspaceRoots) != 1 || !filepath.IsAbs(payload.WorkspaceRoots[0]) {
		return "", errNoRoot
	}
	root, err := repo.FindWorktreeRoot(payload.WorkspaceRoots[0])
	if err != nil {
		return "", err
	}
	if c.Global && cursor.HasProjectHook(payload.WorkspaceRoots[0], c.Event) {
		return "", errElsewhere
	}
	return root, nil
}

func (cursorAdapter) StartsDaemon(hookEvent string) bool { return hookEvent == "beforeSubmitPrompt" }

func (cursorAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return cursor.Parse(c.Event, body, root)
}

func (cursorAdapter) InvalidPayloadMessage() string { return "trace hook: invalid Cursor payload" }

func (cursorAdapter) InstallProject(root string) error { return cursor.Install(root) }
func (cursorAdapter) RemoveProject(root string) error  { return cursor.Uninstall(root) }

func (cursorAdapter) Integration() Integration {
	return Integration{Kind: "CLI/Desktop", Commands: []string{"cursor", "agent"}, Profiles: []string{".cursor"}, Apps: []string{"Cursor.app"}}
}

func (cursorAdapter) SetupGlobal(home string, remove bool) error {
	if remove {
		return cursor.UninstallGlobal(home)
	}
	return cursor.InstallGlobal(home)
}

func (cursorAdapter) LaunchCommand(context string) (string, []string) {
	return "agent", []string{context}
}

func (cursorAdapter) InstallEvidence() []InstallEvidence {
	return []InstallEvidence{{Path: ".cursor/hooks.json", Contains: " hook cursor"}}
}
