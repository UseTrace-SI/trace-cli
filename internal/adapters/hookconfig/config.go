package hookconfig

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

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func paths(root, agent, relative string) (string, string, string) {
	return filepath.Join(root, relative), filepath.Join(root, ".trace", agent+"-hooks.backup"), filepath.Join(root, ".trace", agent+"-hooks.sha")
}

func Install(root, agent, relative string, names []string, grouped bool) error {
	p, backup, hashPath := paths(root, agent, relative)
	original, err := os.ReadFile(p)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return fmt.Errorf("reading %s hooks: %w", agent, err)
	}
	var top map[string]any
	if len(original) > 0 {
		if err := json.Unmarshal(original, &top); err != nil {
			return fmt.Errorf("parsing %s hooks: %w", agent, err)
		}
	}
	if top == nil {
		top = map[string]any{}
	}
	hooks, ok := top["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
	}
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		b := original
		if missing {
			b = []byte("__TRACE_MISSING__")
		}
		if err := os.WriteFile(backup, b, 0600); err != nil {
			return err
		}
	}
	for _, name := range names {
		command := "trace hook " + agent + " " + name
		entries, _ := hooks[name].([]any)
		found := false
		for _, entry := range entries {
			b, _ := json.Marshal(entry)
			if strings.Contains(string(b), command) {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if grouped {
			entries = append(entries, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}})
		} else {
			entries = append(entries, map[string]any{"command": command})
		}
		hooks[name] = entries
	}
	top["hooks"] = hooks
	if agent == "cursor" {
		top["version"] = 1
	}
	b, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		return fmt.Errorf("writing %s hooks: %w", agent, err)
	}
	return os.WriteFile(hashPath, []byte(digest(b)), 0600)
}

func Uninstall(root, agent, relative string, grouped bool) error {
	p, backup, hashPath := paths(root, agent, relative)
	current, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	before, bErr := os.ReadFile(backup)
	installedHash, _ := os.ReadFile(hashPath)
	if bErr == nil && digest(current) == string(installedHash) {
		if bytes.Equal(before, []byte("__TRACE_MISSING__")) {
			return os.Remove(p)
		}
		return os.WriteFile(p, before, 0600)
	}
	var top map[string]any
	if err := json.Unmarshal(current, &top); err != nil {
		return err
	}
	hooks, ok := top["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	for name, raw := range hooks {
		entries, _ := raw.([]any)
		var kept []any
		for _, entry := range entries {
			obj, ok := entry.(map[string]any)
			if !ok {
				kept = append(kept, entry)
				continue
			}
			if grouped {
				children, _ := obj["hooks"].([]any)
				var childKeep []any
				for _, child := range children {
					c, _ := child.(map[string]any)
					command, _ := c["command"].(string)
					if !strings.HasPrefix(command, "trace hook "+agent+" ") {
						childKeep = append(childKeep, child)
					}
				}
				if len(childKeep) > 0 {
					obj["hooks"] = childKeep
					kept = append(kept, obj)
				}
			} else {
				command, _ := obj["command"].(string)
				if !strings.HasPrefix(command, "trace hook "+agent+" ") {
					kept = append(kept, obj)
				}
			}
		}
		if len(kept) > 0 {
			hooks[name] = kept
		} else {
			delete(hooks, name)
		}
	}
	if len(hooks) > 0 {
		top["hooks"] = hooks
	} else {
		delete(top, "hooks")
	}
	if len(top) == 0 {
		return os.Remove(p)
	}
	b, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0600)
}
