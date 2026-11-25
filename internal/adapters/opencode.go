package adapters

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/opencode"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// opencodeAdapter: per repository, the daemon reads OpenCode sessions when
// the opencode.enabled marker exists; Trace's global plugins send generic
// payloads.
type opencodeAdapter struct {
	GenericHooks
	Global
}

func (opencodeAdapter) Name() string { return "opencode" }

func (opencodeAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "OpenCode", Verification: Preview}
}

// opencodeV1Global is the plugin for OpenCode versions before the current
// plugin API.
const opencodeV1Global = "opencode-v1-global"

func (opencodeAdapter) HookAliases() []string { return []string{"opencode-global", opencodeV1Global} }

func (opencodeAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	if !c.Global {
		return "", nil
	}
	_, root, err := payloadCWD(c)
	return root, err
}

// SkipHook: the v1 plugin yields to the daemon's session reader in a
// repository where OpenCode is enabled.
func (opencodeAdapter) SkipHook(c *HookCall, root string) bool {
	if c.Invoked != opencodeV1Global {
		return false
	}
	_, err := os.Stat(filepath.Join(root, ".trace", "opencode.enabled"))
	return err == nil
}

// EnableMarker: trace uninstall has always left this marker in place.
func (opencodeAdapter) EnableMarker() Marker {
	return Marker{File: "opencode.enabled", Label: "OpenCode"}
}

// StartSessionReader reads OpenCode's session database.
func (opencodeAdapter) StartSessionReader(ctx context.Context, root string, logger *slog.Logger, process func(event.Event) error) {
	go func() {
		if err := opencode.Run(ctx, root, "", process); err != nil {
			logger.Error("OpenCode adapter stopped", "error", err)
		}
	}()
}

func (opencodeAdapter) HooksSupersedeSessions() bool { return false }

func (opencodeAdapter) Integration() Integration {
	return Integration{Kind: "CLI/Desktop", Commands: []string{"opencode"}, Profiles: []string{".config/opencode"}, Apps: []string{"OpenCode.app"}}
}

func (opencodeAdapter) SetupGlobal(home string, remove bool) error {
	return opencode.SetupGlobal(home, remove)
}

const opencodeRestart = "Restart OpenCode and confirm the Trace plugin loaded."

func (opencodeAdapter) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return opencodeRestart, nil
}

func (opencodeAdapter) SetupHeadline() (string, bool) { return "OpenCode observers installed.", false }
func (opencodeAdapter) SetupNotice(string) string     { return opencodeRestart }
