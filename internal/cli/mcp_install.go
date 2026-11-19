package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type mcpConfig struct {
	Servers map[string]json.RawMessage `json:"mcpServers"`
}

// atomicWriteMCP replaces path via a synced temporary file and rename, so an
// interrupted write cannot truncate a user's .mcp.json. If path is a symlink,
// the resolved target is replaced instead so the symlink itself is preserved.
// An existing file's permissions are kept; new files are private (0600).
func atomicWriteMCP(path string, data []byte) error {
	target := path
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolving symlink %s: %w", path, err)
		}
		target = resolved
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking %s: %w", path, err)
	}
	mode := os.FileMode(0600)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking mode of %s: %w", target, err)
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".trace-mcp-")
	if err != nil {
		return fmt.Errorf("creating temporary file for %s: %w", target, err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("writing temporary %s: %w", target, err)
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return fmt.Errorf("setting mode on %s: %w", target, err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("syncing temporary %s: %w", target, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing temporary %s: %w", target, err)
	}
	if err := os.Rename(temporary, target); err != nil {
		return fmt.Errorf("replacing %s: %w", target, err)
	}
	return nil
}

func installMCP(root string) error {
	p := filepath.Join(root, ".mcp.json")
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading MCP config: %w", err)
	}
	var cfg mcpConfig
	if len(b) > 0 {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("parsing MCP config: %w", err)
		}
	}
	if cfg.Servers == nil {
		cfg.Servers = map[string]json.RawMessage{}
	}
	if installedName, err := os.ReadFile(filepath.Join(root, ".trace", "mcp-server-name")); err == nil {
		if _, exists := cfg.Servers[string(installedName)]; exists {
			return nil
		}
	}
	name := "trace"
	if _, exists := cfg.Servers[name]; exists {
		name = "trace-usetrace"
		if _, exists := cfg.Servers[name]; exists {
			return nil
		}
	}
	server, err := json.Marshal(map[string]any{"command": "trace", "args": []string{"--repo", root, "mcp"}})
	if err != nil {
		return err
	}
	cfg.Servers[name] = server
	var top map[string]json.RawMessage
	if len(b) > 0 {
		if err := json.Unmarshal(b, &top); err != nil {
			return err
		}
	}
	if top == nil {
		top = map[string]json.RawMessage{}
	}
	servers, err := json.Marshal(cfg.Servers)
	if err != nil {
		return err
	}
	top["mcpServers"] = servers
	b, err = json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteMCP(p, append(b, '\n')); err != nil {
		return fmt.Errorf("writing MCP config: %w", err)
	}
	return os.WriteFile(filepath.Join(root, ".trace", "mcp-server-name"), []byte(name), 0600)
}

func uninstallMCP(root string) error {
	nameBytes, err := os.ReadFile(filepath.Join(root, ".trace", "mcp-server-name"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	p := filepath.Join(root, ".mcp.json")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return err
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(top["mcpServers"], &servers); err != nil {
		return err
	}
	delete(servers, string(nameBytes))
	if len(servers) == 0 {
		delete(top, "mcpServers")
	} else {
		b, err = json.Marshal(servers)
		if err != nil {
			return err
		}
		top["mcpServers"] = b
	}
	if len(top) == 0 {
		return os.Remove(p)
	}
	b, err = json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteMCP(p, append(b, '\n'))
}
