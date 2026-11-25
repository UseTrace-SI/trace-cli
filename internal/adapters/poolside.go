package adapters

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/poolside"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type poolsideAdapter struct {
	Hooks
	Global
}

func (poolsideAdapter) Name() string { return "poolside" }

func (poolsideAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Poolside", Verification: Preview}
}

func (poolsideAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	_, root, err := payloadCWD(c)
	return root, err
}

func (poolsideAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return poolside.Parse(c.Event, body, root)
}

func (poolsideAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"pool"}, Profiles: []string{".config/poolside"}}
}

func (poolsideAdapter) SetupGlobal(home string, remove bool) error {
	return poolside.Setup(home, remove)
}

func (poolsideAdapter) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return poolsideSetupNotice(), nil
}

func (poolsideAdapter) SetupHeadline() (string, bool) { return "Poolside hooks installed.", true }
func (poolsideAdapter) SetupNotice(string) string     { return poolsideSetupNotice() }

var poolVersionPattern = regexp.MustCompile(`^(?:pool\s+)?v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+][0-9A-Za-z.-]+)?$`)

func poolsideSetupNotice() string {
	// Cold executable startup can exceed one second on macOS. Setup is an
	// explicit user operation; allow startup while keeping the probe bounded.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pool", "--version")
	cmd.WaitDelay = 250 * time.Millisecond
	output, err := cmd.Output()
	if err != nil {
		return poolsideVersionNotice("")
	}
	return poolsideVersionNotice(strings.TrimSpace(string(output)))
}

func poolsideVersionNotice(version string) string {
	const instruction = "Poolside 1.0.16 or later is required; restart Poolside after setup."
	match := poolVersionPattern.FindStringSubmatch(version)
	if len(match) != 4 {
		return instruction
	}
	major, e1 := strconv.Atoi(match[1])
	minor, e2 := strconv.Atoi(match[2])
	patch, e3 := strconv.Atoi(match[3])
	if e1 != nil || e2 != nil || e3 != nil {
		return instruction
	}
	prerelease := strings.Contains(strings.SplitN(version, "+", 2)[0], "-")
	if major == 0 || (major == 1 && minor == 0 && (patch < 16 || (patch == 16 && prerelease))) {
		return fmt.Sprintf("Installed Poolside version %s is too old. %s", version, instruction)
	}
	return instruction
}
