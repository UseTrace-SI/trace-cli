package adapters

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/kiro"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

type kiroAdapter struct {
	Hooks
	Global
}

func (kiroAdapter) Name() string { return "kiro" }

func (kiroAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Kiro", Verification: Preview}
}

func (kiroAdapter) HookAliases() []string { return []string{"kiro-global"} }

// ResolveHookRoot: a global hook yields to the project's own Trace hook.
func (kiroAdapter) ResolveHookRoot(c *HookCall) (string, error) {
	b, err := c.Body()
	if err != nil {
		return "", err
	}
	root, err := kiroHookRoot(b)
	if err != nil {
		return "", err
	}
	if c.Global && kiro.HasProjectHook(root, c.Event) {
		return "", errElsewhere
	}
	return root, nil
}

// kiroHookRoot: Kiro's hook payload can identify a different workspace than
// the process cwd. Older payloads that omit cwd retain the documented
// project-directory fallback.
func kiroHookRoot(body []byte) (string, error) {
	var payload struct {
		CWD string `json:"cwd"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parsing Kiro hook cwd: %w", err)
	}
	dir := payload.CWD
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("finding Kiro hook directory: %w", err)
		}
	}
	return repo.FindWorktreeRoot(dir)
}

// DecodeHook: Kiro passes the prompt in USER_PROMPT.
func (kiroAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return kiro.Parse(c.Event, body, root, os.Getenv("USER_PROMPT"))
}

func (kiroAdapter) InstallProject(root string) error { return kiro.Install(root) }
func (kiroAdapter) RemoveProject(root string) error  { return kiro.Uninstall(root) }

func (kiroAdapter) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"kiro-cli", "kiro"}, Profiles: []string{".kiro"}}
}

func (kiroAdapter) SetupGlobal(home string, remove bool) error {
	if remove {
		return kiro.Uninstall(home)
	}
	return kiro.InstallGlobal(home)
}

const kiroRestart = "Restart Kiro and confirm hooks are active."

func (kiroAdapter) SetupJSONNotice(_ string, remove bool) (string, map[string]any) {
	if remove {
		return "", nil
	}
	return kiroRestart, nil
}

func (kiroAdapter) SetupHeadline() (string, bool) { return "Kiro hooks installed.", true }
func (kiroAdapter) SetupNotice(string) string     { return kiroRestart }

func (kiroAdapter) InstallEvidence() []InstallEvidence {
	return []InstallEvidence{{Path: ".kiro/hooks/trace.json", Contains: "Trace managed hook"}}
}
