package kilo

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
)

//go:embed plugin.ts
var plugin string

//go:embed global.ts
var globalPlugin string

//go:embed global_v1.ts
var previousGlobalPlugin string

//go:embed global_v2.ts
var previousGlobalPluginV2 string

//go:embed global_v3.ts
var previousGlobalPluginV3 string

const relative = ".kilo/plugin/trace.ts"

func projectPlugin(binary, root string) []byte {
	data := strings.ReplaceAll(plugin, "__TRACE_BIN__", strconv.Quote(binary))
	return []byte(strings.ReplaceAll(data, "__TRACE_ROOT__", strconv.Quote(root)))
}

func managedProject(data []byte) bool {
	values := make([]string, 0, 2)
	for _, prefix := range []string{"const traceBin = ", "const repoRoot = "} {
		text := string(data)
		start := strings.Index(text, prefix)
		if start < 0 {
			return false
		}
		value := text[start+len(prefix):]
		end := strings.Index(value, ";\n")
		if end < 0 {
			return false
		}
		parsed, err := strconv.Unquote(value[:end])
		if err != nil || parsed == "" {
			return false
		}
		values = append(values, parsed)
	}
	return bytes.Equal(data, projectPlugin(values[0], values[1]))
}

func globalPath(home string) (string, error) {
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(config) {
		return "", fmt.Errorf("XDG_CONFIG_HOME must be absolute")
	}
	return filepath.Join(config, "kilo", "plugin", "trace-global.ts"), nil
}

func renderGlobal(binary string) []byte {
	return []byte(strings.ReplaceAll(globalPlugin, "__TRACE_BIN__", strconv.Quote(binary)))
}

func managedGlobal(data []byte) bool {
	const prefix = "const traceBin = "
	text := string(data)
	start := strings.Index(text, prefix)
	if start < 0 {
		return false
	}
	value := text[start+len(prefix):]
	end := strings.Index(value, ";\n")
	if end < 0 {
		return false
	}
	previous, err := strconv.Unquote(value[:end])
	if err != nil || previous == "" {
		return false
	}
	for _, version := range []string{globalPlugin, previousGlobalPlugin, previousGlobalPluginV2, previousGlobalPluginV3} {
		if bytes.Equal(data, []byte(strings.ReplaceAll(version, "__TRACE_BIN__", strconv.Quote(previous)))) {
			return true
		}
	}
	return false
}

// SetupGlobal installs a machine-wide Kilo plugin without replacing user code.
func SetupGlobal(home string, remove bool) error {
	p, err := globalPath(home)
	if err != nil {
		return err
	}
	data := renderGlobal(hookcmd.Path())
	info, err := os.Lstat(p)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular Kilo plugin at %s", p)
		}
		old, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("reading Kilo plugin: %w", err)
		}
		if !bytes.Equal(old, data) && !managedGlobal(old) {
			return fmt.Errorf("refusing to replace modified Kilo plugin at %s", p)
		}
		if remove {
			return os.Remove(p)
		}
		if !bytes.Equal(old, data) {
			if err := os.WriteFile(p, data, 0600); err != nil {
				return fmt.Errorf("updating Kilo plugin: %w", err)
			}
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("checking Kilo plugin: %w", err)
	}
	if remove {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating Kilo plugin directory: %w", err)
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		return fmt.Errorf("writing Kilo plugin: %w", err)
	}
	return nil
}

func Install(root string) error {
	path := filepath.Join(root, relative)
	data := projectPlugin(hookcmd.Path(), root)
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular Kilo plugin at %s", path)
		}
		old, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading Kilo plugin: %w", err)
		}
		if !managedProject(old) {
			return fmt.Errorf("refusing to replace modified Kilo plugin at %s", path)
		}
		if bytes.Equal(old, data) {
			return nil
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			return fmt.Errorf("updating Kilo plugin: %w", err)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("checking Kilo plugin: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating Kilo plugin directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("creating Kilo plugin: %w", err)
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("writing Kilo plugin: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing Kilo plugin: %w", closeErr)
	}
	return nil
}

func Uninstall(root string) error {
	path := filepath.Join(root, relative)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking Kilo plugin: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to remove non-regular Kilo plugin at %s", path)
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading Kilo plugin: %w", err)
	}
	if managedProject(b) {
		return os.Remove(path)
	}
	return fmt.Errorf("refusing to remove modified Kilo plugin at %s", path)
}
