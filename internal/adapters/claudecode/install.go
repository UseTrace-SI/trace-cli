package claudecode

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}
type group struct {
	Matcher string `json:"matcher,omitempty"`
	Hooks   []hook `json:"hooks"`
}

var events = []struct{ name, matcher string }{{"SessionStart", ""}, {"UserPromptSubmit", ""}, {"PostToolUse", "Edit|Write|MultiEdit|NotebookEdit|Bash"}, {"Stop", ""}, {"SessionEnd", ""}}

func settingsPath(root string) string { return filepath.Join(root, ".claude", "settings.local.json") }
func backupPath(root string) string   { return filepath.Join(root, ".trace", "claude-settings.backup") }
func hashBytes(b []byte) string       { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func Install(root string) error {
	p := settingsPath(root)
	original, err := os.ReadFile(p)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return fmt.Errorf("reading Claude settings: %w", err)
	}
	var settings map[string]json.RawMessage
	if len(original) > 0 {
		if err := json.Unmarshal(original, &settings); err != nil {
			return fmt.Errorf("parsing Claude settings: %w", err)
		}
	}
	if settings == nil {
		settings = map[string]json.RawMessage{}
	}
	var hooks map[string][]group
	if len(settings["hooks"]) > 0 {
		if err := json.Unmarshal(settings["hooks"], &hooks); err != nil {
			return fmt.Errorf("parsing Claude hooks: %w", err)
		}
	}
	if hooks == nil {
		hooks = map[string][]group{}
	}
	if _, err := os.Stat(backupPath(root)); os.IsNotExist(err) {
		if missing {
			original = []byte("__TRACE_MISSING__")
		}
		if err := os.WriteFile(backupPath(root), original, 0600); err != nil {
			return fmt.Errorf("backing up Claude settings: %w", err)
		}
	}
	for _, ev := range events {
		command := "trace hook claude-code " + ev.name
		found := false
		for _, g := range hooks[ev.name] {
			for _, h := range g.Hooks {
				if h.Command == command {
					found = true
				}
			}
		}
		if !found {
			hooks[ev.name] = append(hooks[ev.name], group{Matcher: ev.matcher, Hooks: []hook{{Type: "command", Command: command}}})
		}
	}
	b, err := json.MarshalIndent(hooks, "", "  ")
	if err != nil {
		return err
	}
	settings["hooks"] = b
	b, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating Claude settings directory: %w", err)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		return fmt.Errorf("writing Claude settings: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".trace", "claude-settings.installed-sha"), []byte(hashBytes(b)), 0600); err != nil {
		return fmt.Errorf("recording Claude settings hash: %w", err)
	}
	return nil
}

func Uninstall(root string) error {
	p := settingsPath(root)
	current, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading Claude settings: %w", err)
	}
	installedHash, _ := os.ReadFile(filepath.Join(root, ".trace", "claude-settings.installed-sha"))
	backup, backupErr := os.ReadFile(backupPath(root))
	if backupErr == nil && hashBytes(current) == string(installedHash) {
		if bytes.Equal(backup, []byte("__TRACE_MISSING__")) {
			return os.Remove(p)
		}
		return os.WriteFile(p, backup, 0600)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(current, &settings); err != nil {
		return fmt.Errorf("parsing Claude settings: %w", err)
	}
	var hooks map[string][]group
	if err := json.Unmarshal(settings["hooks"], &hooks); err != nil {
		return fmt.Errorf("parsing Claude hooks: %w", err)
	}
	for name, groups := range hooks {
		var kept []group
		for _, g := range groups {
			var hh []hook
			for _, h := range g.Hooks {
				if !strings.HasPrefix(h.Command, "trace hook claude-code ") {
					hh = append(hh, h)
				}
			}
			if len(hh) > 0 {
				g.Hooks = hh
				kept = append(kept, g)
			}
		}
		if len(kept) == 0 {
			delete(hooks, name)
		} else {
			hooks[name] = kept
		}
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		b, _ := json.Marshal(hooks)
		settings["hooks"] = b
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0600)
}
