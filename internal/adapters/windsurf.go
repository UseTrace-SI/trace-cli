package adapters

import (
	"os"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/windsurf"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

type windsurfAdapter struct {
	Hooks
	Global
}

func (windsurfAdapter) Name() string { return "windsurf" }

func (windsurfAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Windsurf Cascade", Verification: Preview}
}

func (windsurfAdapter) HookAliases() []string { return []string{"windsurf-global"} }

// ResolveHookRoot: Windsurf payloads carry no directory; a global hook uses
// the process directory and defers to project hooks there.
func (windsurfAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	if !c.Global {
		return "", nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, err := repo.FindWorktreeRoot(cwd)
	if err != nil {
		return "", err
	}
	if windsurf.HasProjectHook(root, c.Event) || (cwd != root && windsurf.HasProjectHook(cwd, c.Event)) {
		return "", errElsewhere
	}
	return root, nil
}

func (windsurfAdapter) StartsDaemon(hookEvent string) bool { return hookEvent == "pre_user_prompt" }

func (windsurfAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return windsurf.Parse(c.Event, body, root)
}

func (windsurfAdapter) InstallProject(root string) error { return windsurf.Install(root) }
func (windsurfAdapter) RemoveProject(root string) error  { return windsurf.Uninstall(root) }

func (windsurfAdapter) Integration() Integration {
	return Integration{Kind: "Desktop", Commands: []string{"windsurf"}, Profiles: []string{".codeium/windsurf"}, Apps: []string{"Windsurf.app", "Devin.app"}}
}

func (windsurfAdapter) SetupGlobal(home string, remove bool) error {
	if remove {
		return windsurf.UninstallGlobal(home)
	}
	return windsurf.InstallGlobal(home)
}

const windsurfRestart = "Restart Windsurf or Devin Desktop and confirm hooks are active."

func (windsurfAdapter) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return windsurfRestart, nil
}

func (windsurfAdapter) SetupHeadline() (string, bool) { return "Windsurf hooks installed.", false }
func (windsurfAdapter) SetupNotice(string) string     { return windsurfRestart }
