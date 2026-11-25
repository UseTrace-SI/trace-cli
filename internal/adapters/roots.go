package adapters

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

// errElsewhere drops a callback that another hook (usually the project's
// own Trace hook for the same agent) reports instead.
var errElsewhere = errors.New("hook callback is reported by another hook")

// errNoRoot drops a callback whose payload does not identify a worktree.
var errNoRoot = errors.New("hook payload does not identify a worktree")

// payloadCWD returns the payload's absolute "cwd" and its worktree root.
func payloadCWD(c *HookCall) (cwd, root string, err error) {
	b, err := c.Body()
	if err != nil {
		return "", "", err
	}
	var payload struct {
		CWD string `json:"cwd"`
	}
	if json.Unmarshal(b, &payload) != nil || !filepath.IsAbs(payload.CWD) {
		return "", "", errNoRoot
	}
	root, err = repo.FindWorktreeRoot(payload.CWD)
	if err != nil {
		return "", "", err
	}
	return payload.CWD, root, nil
}

// cwdHookRoot routes by the payload's "cwd". A global hook defers to a
// project hook installed at the worktree root or at the working directory.
func cwdHookRoot(c *HookCall, hasProjectHook func(root, name string) bool) (string, error) {
	cwd, root, err := payloadCWD(c)
	if err != nil {
		return "", err
	}
	if c.Global && hasProjectHook != nil && (hasProjectHook(root, c.Event) || (cwd != root && hasProjectHook(cwd, c.Event))) {
		return "", errElsewhere
	}
	return root, nil
}
