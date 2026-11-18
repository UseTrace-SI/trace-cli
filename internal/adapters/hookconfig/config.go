package hookconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
)

// Preserve JSON numbers exactly when merging configuration owned by agents.
func decodeObject(data []byte, out *map[string]any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func isVersionOne(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	v, err := number.Float64()
	return err == nil && v == 1
}

func rewriteHookCommands(value any, agent, name, command string) bool {
	found := false
	switch node := value.(type) {
	case map[string]any:
		if current, ok := node["command"].(string); ok && hookcmd.IsTraceHook(current, agent) && hookcmd.IsTraceHookFor(current, agent, name) {
			node["command"] = command
			found = true
		}
		for key, child := range node {
			if key == "command" {
				continue
			}
			if rewriteHookCommands(child, agent, name, command) {
				found = true
			}
		}
	case []any:
		for _, child := range node {
			if rewriteHookCommands(child, agent, name, command) {
				found = true
			}
		}
	}
	return found
}

func paths(root, agent, relative string) (string, string, string) {
	return filepath.Join(root, relative), filepath.Join(root, ".trace", agent+"-hooks.backup"), filepath.Join(root, ".trace", agent+"-hooks.sha")
}

// atomicWrite replaces path via a synced temporary file and rename, so an
// interrupted write cannot truncate a user's configuration. An existing
// file's permissions are preserved; new files are private (0600).
func atomicWrite(path string, data []byte) error {
	mode := os.FileMode(0600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking mode of %s: %w", path, err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".trace-hook-")
	if err != nil {
		return fmt.Errorf("creating temporary file for %s: %w", path, err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("writing temporary %s: %w", path, err)
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return fmt.Errorf("setting mode on %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("syncing temporary %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing temporary %s: %w", path, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

func Install(root, agent, relative string, names []string, grouped bool) error {
	return install(root, agent, relative, names, grouped, false, nil)
}

// InstallWithMatchers merges grouped hooks with event-specific matchers.
func InstallWithMatchers(root, agent, relative string, names []string, matchers map[string]string) error {
	return install(root, agent, relative, names, true, false, matchers)
}

// InstallFlat installs grouped hooks in a standalone event map such as
// Factory Droid's .factory/hooks.json (which has no enclosing "hooks" key).
func InstallFlat(root, agent, relative string, names []string) error {
	return install(root, agent, relative, names, true, true, nil)
}

func install(root, agent, relative string, names []string, grouped, flat bool, matchers map[string]string) error {
	p, _, _ := paths(root, agent, relative)
	original, err := os.ReadFile(p)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return fmt.Errorf("reading %s hooks: %w", agent, err)
	}
	var top map[string]any
	if len(original) > 0 {
		if err := decodeObject(original, &top); err != nil {
			return fmt.Errorf("parsing %s hooks: %w", agent, err)
		}
		if top == nil {
			return fmt.Errorf("invalid %s settings: expected an object", agent)
		}
	}
	if top == nil {
		top = map[string]any{}
	}
	if agent == "cursor" || agent == "cursor-global" || agent == "copilot" || agent == "copilot-global" {
		if version, exists := top["version"]; exists && !isVersionOne(version) {
			return fmt.Errorf("unsupported %s hooks version; configuration was not changed", agent)
		}
	}
	hooks := top
	if !flat {
		if value, exists := top["hooks"]; exists {
			var ok bool
			hooks, ok = value.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid %s hooks: expected an object", agent)
			}
		} else {
			hooks = map[string]any{}
		}
	}
	for _, name := range names {
		if value, exists := hooks[name]; exists {
			if _, ok := value.([]any); !ok {
				return fmt.Errorf("invalid %s %s hooks: expected an array", agent, name)
			}
		}
	}
	for _, name := range names {
		command := hookcmd.Command(agent, name)
		entries, _ := hooks[name].([]any)
		found := rewriteHookCommands(entries, agent, name, command)
		if found {
			hooks[name] = entries
			continue
		}
		if grouped {
			group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}
			if matcher := matchers[name]; matcher != "" {
				group["matcher"] = matcher
			}
			entries = append(entries, group)
		} else {
			entry := map[string]any{"command": command}
			if agent == "copilot" || agent == "copilot-global" {
				entry["type"] = "command"
			}
			entries = append(entries, entry)
		}
		hooks[name] = entries
	}
	if !flat {
		top["hooks"] = hooks
	}
	if agent == "cursor" || agent == "cursor-global" || agent == "copilot" || agent == "copilot-global" {
		top["version"] = json.Number("1")
	}
	b, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	if err := atomicWrite(p, b); err != nil {
		return fmt.Errorf("writing %s hooks: %w", agent, err)
	}
	return nil
}

func Uninstall(root, agent, relative string, grouped bool) error {
	return uninstall(root, agent, relative, grouped, false)
}

func UninstallFlat(root, agent, relative string) error {
	return uninstall(root, agent, relative, true, true)
}

func uninstall(root, agent, relative string, grouped, flat bool) error {
	p, backup, hashPath := paths(root, agent, relative)
	cleanup := func() error {
		for _, path := range []string{backup, hashPath} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing %s hook state: %w", agent, err)
			}
		}
		return nil
	}
	current, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var top map[string]any
	if err := decodeObject(current, &top); err != nil {
		return err
	}
	hooks := top
	if !flat {
		var ok bool
		hooks, ok = top["hooks"].(map[string]any)
		if !ok {
			return nil
		}
	}
	removedAny := false
	for name, raw := range hooks {
		entries, ok := raw.([]any)
		if !ok {
			continue
		}
		var kept []any
		removedEvent := false
		for _, entry := range entries {
			obj, ok := entry.(map[string]any)
			if !ok {
				kept = append(kept, entry)
				continue
			}
			if grouped {
				children, ok := obj["hooks"].([]any)
				if !ok {
					kept = append(kept, entry)
					continue
				}
				var childKeep []any
				removed := false
				for _, child := range children {
					c, _ := child.(map[string]any)
					command, _ := c["command"].(string)
					if !hookcmd.IsTraceHook(command, agent) {
						childKeep = append(childKeep, child)
					} else {
						removed = true
						removedEvent = true
					}
				}
				if !removed {
					kept = append(kept, entry)
				} else if len(childKeep) > 0 {
					obj["hooks"] = childKeep
					kept = append(kept, obj)
				}
			} else {
				command, _ := obj["command"].(string)
				if !hookcmd.IsTraceHook(command, agent) {
					kept = append(kept, obj)
				} else {
					removedEvent = true
				}
			}
		}
		if !removedEvent {
			continue
		}
		removedAny = true
		if len(kept) > 0 {
			hooks[name] = kept
		} else {
			delete(hooks, name)
		}
	}
	if !removedAny {
		return nil
	}
	if !flat {
		if len(hooks) > 0 {
			top["hooks"] = hooks
		} else {
			delete(top, "hooks")
		}
	}
	if len(top) == 0 {
		if err := os.Remove(p); err != nil {
			return err
		}
		return cleanup()
	}
	b, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(p, append(b, '\n')); err != nil {
		return err
	}
	return cleanup()
}
