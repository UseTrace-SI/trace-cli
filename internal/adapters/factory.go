package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/factory"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

type factoryAdapter struct {
	Hooks
	Global
}

func (factoryAdapter) Name() string { return "factory" }

func (factoryAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Factory Droid", Verification: Preview}
}

func (factoryAdapter) HookAliases() []string { return []string{"factory-global"} }

// ResolveHookRoot prefers FACTORY_PROJECT_DIR over the payload's cwd. A
// global hook yields to a project hook at the root or that directory.
func (factoryAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	b, err := c.Body()
	if err != nil {
		return "", err
	}
	var payload struct {
		CWD string `json:"cwd"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return "", err
	}
	directory := os.Getenv("FACTORY_PROJECT_DIR")
	if directory == "" {
		directory = payload.CWD
	}
	if !filepath.IsAbs(directory) {
		return "", errNoRoot
	}
	root, err := repo.FindWorktreeRoot(directory)
	if err != nil {
		return "", err
	}
	if c.Global && (factory.HasProjectHook(root, c.Event) || (directory != root && factory.HasProjectHook(directory, c.Event))) {
		return "", errElsewhere
	}
	return root, nil
}

func (factoryAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return factory.Parse(c.Event, body, root)
}

func (factoryAdapter) InstallProject(root string) error { return factory.Install(root) }
func (factoryAdapter) RemoveProject(root string) error  { return factory.Uninstall(root) }

func (factoryAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"droid"}, Profiles: []string{".factory"}}
}

func (factoryAdapter) SetupGlobal(home string, remove bool) error {
	if remove {
		return factory.UninstallGlobal(home)
	}
	return factory.InstallGlobal(home)
}
