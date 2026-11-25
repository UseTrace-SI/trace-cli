package adapters

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/openhands"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

type openhandsAdapter struct {
	Hooks
	Global
}

func (openhandsAdapter) Name() string { return "openhands" }

func (openhandsAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "OpenHands CLI", Verification: Preview}
}

func (openhandsAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	b, err := c.Body()
	if err != nil {
		return "", err
	}
	return openhandsHookRoot(b)
}

// openhandsHookRoot routes by the SDK's working directory, falling back to
// the process directory.
func openhandsHookRoot(body []byte) (string, error) {
	var payload struct {
		WorkingDir string `json:"working_dir"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parsing OpenHands hook directory: %w", err)
	}
	dir := payload.WorkingDir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("finding OpenHands hook directory: %w", err)
		}
	}
	return repo.FindWorktreeRoot(dir)
}

func (openhandsAdapter) StartsDaemon(hookEvent string) bool { return hookEvent == "user_prompt_submit" }

func (openhandsAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return openhands.Parse(c.Event, body, root)
}

// CheckProject: OpenHands project hooks merge into an existing .openhands.
func (openhandsAdapter) CheckProject(root string) error   { return openhands.CheckProject(root) }
func (openhandsAdapter) InstallProject(root string) error { return openhands.InstallProject(root) }
func (openhandsAdapter) RemoveProject(root string) error  { return openhands.UninstallProject(root) }

func (openhandsAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"openhands"}, Profiles: []string{".openhands"}}
}

func (openhandsAdapter) SetupGlobal(home string, remove bool) error {
	return openhands.Setup(home, remove)
}
