package adapters

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/auggie"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

var auggieGlobalEvents = []string{"SessionStart", "PromptSubmit", "PostToolUse", "Stop", "SessionEnd"}

type auggieAdapter struct {
	Hooks
	Global
}

func (auggieAdapter) Name() string { return "auggie" }

func (auggieAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Auggie", Verification: Preview}
}

func (auggieAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	b, err := c.Body()
	if err != nil {
		return "", err
	}
	return auggieHookRoot(b)
}

// auggieHookRoot: Auggie can run with --workspace-root while its hook
// process has a different working directory. Only route multiple
// workspaces when they share a worktree.
func auggieHookRoot(body []byte) (string, error) {
	var payload struct {
		CWD            string   `json:"cwd"`
		WorkspaceRoots []string `json:"workspace_roots"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parsing Auggie hook cwd: %w", err)
	}
	projects := payload.WorkspaceRoots
	if len(projects) == 0 {
		projects = []string{payload.CWD}
	}
	var root string
	for _, project := range projects {
		if !filepath.IsAbs(project) {
			return "", fmt.Errorf("auggie workspace must be absolute")
		}
		workspaceRoot, err := repo.FindWorktreeRoot(project)
		if err != nil {
			return "", fmt.Errorf("finding Auggie workspace: %w", err)
		}
		if root != "" && root != workspaceRoot {
			return "", fmt.Errorf("auggie workspaces span multiple worktrees")
		}
		root = workspaceRoot
	}
	return root, nil
}

func (auggieAdapter) StartsDaemon(hookEvent string) bool { return hookEvent == "PromptSubmit" }

func (auggieAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return auggie.Parse(c.Event, body, root)
}

func (auggieAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"auggie"}, Profiles: []string{".augment"}}
}

func (auggieAdapter) SetupNeedsStateDir() bool { return true }

func (a auggieAdapter) SetupGlobal(home string, remove bool) error {
	if remove {
		return hookconfig.Uninstall(home, a.Name(), ".augment/settings.json", true)
	}
	return hookconfig.Install(home, a.Name(), ".augment/settings.json", auggieGlobalEvents, true)
}
