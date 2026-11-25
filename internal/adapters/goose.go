package adapters

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/goose"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

type gooseAdapter struct{ Hooks }

func (gooseAdapter) Name() string { return "goose" }

func (gooseAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Goose", Verification: Preview}
}

// ResolveHookRoot: Goose exposes the directory of the plugin that launched
// a hook. Its process cwd can belong to a different session, particularly
// in the desktop server.
func (gooseAdapter) ResolveHookRoot(*HookCall) (string, error) {
	if plugin := os.Getenv("PLUGIN_ROOT"); plugin != "" {
		return gooseProjectRoot(plugin)
	}
	return "", nil
}

func gooseProjectRoot(pluginRoot string) (string, error) {
	if !filepath.IsAbs(pluginRoot) {
		return "", fmt.Errorf("goose plugin root must be absolute")
	}
	pluginRoot = filepath.Clean(pluginRoot)
	if filepath.Base(pluginRoot) != "trace" || filepath.Base(filepath.Dir(pluginRoot)) != "plugins" || filepath.Base(filepath.Dir(filepath.Dir(pluginRoot))) != ".agents" {
		return "", fmt.Errorf("not a Trace project plugin")
	}
	project := filepath.Dir(filepath.Dir(filepath.Dir(pluginRoot)))
	root, err := repo.FindWorktreeRoot(project)
	if err != nil {
		return "", err
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil || project != root {
		return "", fmt.Errorf("goose plugin is not at the worktree root")
	}
	return root, nil
}

func (gooseAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return goose.Parse(c.Event, body, root)
}

func (gooseAdapter) InstallProject(root string) error { return goose.Install(root) }
func (gooseAdapter) RemoveProject(root string) error  { return goose.Uninstall(root) }
