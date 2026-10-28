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
	if err := os.WriteFile(p, append(b, '\n'), 0600); err != nil {
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
	return os.WriteFile(p, append(b, '\n'), 0600)
}
