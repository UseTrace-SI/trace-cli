package agy

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

const hookPrefix = "trace hook agy "

func hooksPath(root string) string       { return filepath.Join(root, ".agents", "hooks.json") }
func statePath(root, name string) string { return filepath.Join(root, ".trace", "agy-hooks."+name) }
func hash(b []byte) string               { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func ownHooks() map[string]any {
	handler := func(name string) map[string]any {
		return map[string]any{"type": "command", "command": hookPrefix + name, "timeout": 1}
	}
	return map[string]any{
		"SessionStart":  []any{handler("SessionStart")},
		"PreInvocation": []any{handler("PreInvocation")},
		"PostToolUse": []any{map[string]any{
			"matcher": "write_to_file|replace_file_content|multi_replace_file_content|run_command",
			"hooks":   []any{handler("PostToolUse")},
		}},
		"Stop": []any{handler("Stop")},
	}
}

func Install(root string) error {
	p := hooksPath(root)
	original, err := os.ReadFile(p)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return fmt.Errorf("reading Antigravity hooks: %w", err)
	}
	settings := map[string]json.RawMessage{}
	if len(original) > 0 {
		if err := json.Unmarshal(original, &settings); err != nil {
			return fmt.Errorf("parsing Antigravity hooks: %w", err)
		}
	}
	if settings == nil {
		settings = map[string]json.RawMessage{}
	}
	for _, value := range settings {
		if bytes.Contains(value, []byte(hookPrefix)) {
			return nil
		}
	}
	name := "usetrace"
	for i := 2; settings[name] != nil; i++ {
		name = fmt.Sprintf("usetrace-%d", i)
	}
	b, err := json.MarshalIndent(ownHooks(), "", "  ")
	if err != nil {
		return fmt.Errorf("encoding Trace hooks: %w", err)
	}
	settings[name] = b
	b, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding Antigravity hooks: %w", err)
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating Antigravity hooks directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		return fmt.Errorf("creating Trace directory: %w", err)
	}
	backup := original
	if missing {
		backup = []byte("__TRACE_MISSING__")
	}
	if _, err := os.Stat(statePath(root, "backup")); os.IsNotExist(err) {
		if err := os.WriteFile(statePath(root, "backup"), backup, 0600); err != nil {
			return fmt.Errorf("backing up Antigravity hooks: %w", err)
		}
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		return fmt.Errorf("writing Antigravity hooks: %w", err)
	}
	if err := os.WriteFile(statePath(root, "installed-sha"), []byte(hash(b)), 0600); err != nil {
		return fmt.Errorf("recording Antigravity hook hash: %w", err)
	}
	if err := os.WriteFile(statePath(root, "name"), []byte(name), 0600); err != nil {
		return fmt.Errorf("recording Antigravity hook name: %w", err)
	}
	return nil
}

func Uninstall(root string) error {
	p := hooksPath(root)
	current, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading Antigravity hooks: %w", err)
	}
	installedHash, _ := os.ReadFile(statePath(root, "installed-sha"))
	backup, backupErr := os.ReadFile(statePath(root, "backup"))
	if backupErr == nil && hash(current) == string(installedHash) {
		if bytes.Equal(backup, []byte("__TRACE_MISSING__")) {
			return os.Remove(p)
		}
		return os.WriteFile(p, backup, 0600)
	}
	settings := map[string]json.RawMessage{}
	if err := json.Unmarshal(current, &settings); err != nil {
		return fmt.Errorf("parsing Antigravity hooks: %w", err)
	}
	for name, raw := range settings {
		if !bytes.Contains(raw, []byte(hookPrefix)) {
			continue
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entry); err != nil {
			return fmt.Errorf("parsing Trace hook entry: %w", err)
		}
		for event, handlers := range entry {
			var groups []map[string]json.RawMessage
			if err := json.Unmarshal(handlers, &groups); err != nil {
				continue
			}
			kept := make([]map[string]json.RawMessage, 0, len(groups))
			for _, group := range groups {
				if commandIsOurs(group["command"]) {
					continue
				}
				if nested, ok := group["hooks"]; ok {
					var hooks []map[string]json.RawMessage
					if json.Unmarshal(nested, &hooks) == nil {
						filtered := make([]map[string]json.RawMessage, 0, len(hooks))
						for _, hook := range hooks {
							if !commandIsOurs(hook["command"]) {
								filtered = append(filtered, hook)
							}
						}
						if len(filtered) == 0 {
							continue
						}
						group["hooks"], _ = json.Marshal(filtered)
					}
				}
				kept = append(kept, group)
			}
			if len(kept) == 0 {
				delete(entry, event)
			} else {
				entry[event], _ = json.Marshal(kept)
			}
		}
		if len(entry) == 0 {
			delete(settings, name)
		} else {
			settings[name], _ = json.Marshal(entry)
		}
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding remaining Antigravity hooks: %w", err)
	}
	return os.WriteFile(p, append(b, '\n'), 0600)
}

func commandIsOurs(raw json.RawMessage) bool {
	var command string
	return json.Unmarshal(raw, &command) == nil && strings.HasPrefix(command, hookPrefix)
}
