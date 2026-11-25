package adapters

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/openclaude"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type openclaudeAdapter struct {
	Hooks
	Global
}

func (openclaudeAdapter) Name() string { return "openclaude" }

func (openclaudeAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "OpenClaude", Verification: Preview}
}

func (openclaudeAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	_, root, err := payloadCWD(c)
	return root, err
}

func (openclaudeAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return openclaude.Parse(c.Event, body, root)
}

func (openclaudeAdapter) InstallProject(root string) error { return openclaude.Install(root) }
func (openclaudeAdapter) RemoveProject(root string) error  { return openclaude.Uninstall(root) }

func (openclaudeAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"openclaude"}, Profiles: []string{".openclaude"}}
}

// SetupGlobal honors OPENCLAUDE_CONFIG_DIR.
func (openclaudeAdapter) SetupGlobal(home string, remove bool) error {
	configRoot := os.Getenv("OPENCLAUDE_CONFIG_DIR")
	if configRoot == "" {
		configRoot = filepath.Join(home, ".openclaude")
	}
	var err error
	configRoot, err = filepath.Abs(configRoot)
	if err != nil {
		return fmt.Errorf("resolving OpenClaude config directory: %w", err)
	}
	if remove {
		return openclaude.UninstallGlobal(configRoot)
	}
	if err := os.MkdirAll(filepath.Join(configRoot, ".trace"), 0700); err != nil {
		return fmt.Errorf("creating OpenClaude setup state: %w", err)
	}
	return openclaude.InstallGlobal(configRoot)
}

func (openclaudeAdapter) InstallEvidence() []InstallEvidence {
	return []InstallEvidence{{Path: ".openclaude/settings.local.json", Contains: " hook openclaude"}}
}
