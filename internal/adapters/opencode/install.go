package opencode

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

//go:embed global.ts
var globalPlugin string

//go:embed global_v1.ts
var previousGlobalPlugin string

//go:embed global_v2.ts
var previousGlobalPluginV2 string

//go:embed legacy_plugin.ts
var legacyPlugin string

func globalPath(home string) (string, error) {
	config := os.Getenv("OPENCODE_CONFIG_DIR")
	if config == "" {
		config = os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		config = filepath.Join(config, "opencode")
	}
	if !filepath.IsAbs(config) {
		return "", fmt.Errorf("OpenCode config directory must be absolute")
	}
	return filepath.Join(config, "plugins", "usetrace", "index.ts"), nil
}

func renderGlobal(binary string) []byte {
	return []byte(strings.ReplaceAll(globalPlugin, "__TRACE_BIN__", strconv.Quote(binary)))
}

func legacyPath(home string) (string, error) {
	path, err := globalPath(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(filepath.Dir(path)), "usetrace-v1.ts"), nil
}

func renderLegacy(binary string) []byte {
	return []byte(strings.ReplaceAll(legacyPlugin, "__TRACE_BIN__", strconv.Quote(binary)))
}

func managedLegacy(data []byte) bool {
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
	return err == nil && previous != "" && bytes.Equal(data, renderLegacy(previous))
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
	for _, version := range []string{globalPlugin, previousGlobalPlugin, previousGlobalPluginV2} {
		if bytes.Equal(data, []byte(strings.ReplaceAll(version, "__TRACE_BIN__", strconv.Quote(previous)))) {
			return true
		}
	}
	return false
}

// SetupGlobal installs a Trace-owned OpenCode v2 plugin without replacing user files.
func SetupGlobal(home string, remove bool) error {
	legacy, err := legacyPath(home)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(legacy); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular OpenCode v1 plugin at %s", legacy)
		}
		old, err := os.ReadFile(legacy)
		if err != nil {
			return fmt.Errorf("reading OpenCode v1 plugin: %w", err)
		}
		if !managedLegacy(old) {
			return fmt.Errorf("refusing to replace modified OpenCode v1 plugin at %s", legacy)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking OpenCode v1 plugin: %w", err)
	}
	if err := setupV2(home, remove); err != nil {
		return err
	}
	if remove {
		if err := os.Remove(legacy); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing OpenCode v1 plugin: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		return fmt.Errorf("creating OpenCode v1 plugin directory: %w", err)
	}
	if err := os.WriteFile(legacy, renderLegacy(hookcmd.Path()), 0600); err != nil {
		return fmt.Errorf("writing OpenCode v1 plugin: %w", err)
	}
	return nil
}

func setupV2(home string, remove bool) error {
	path, err := globalPath(home)
	if err != nil {
		return err
	}
	data := renderGlobal(hookcmd.Path())
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular OpenCode plugin at %s", path)
		}
		old, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading OpenCode plugin: %w", err)
		}
		if !managedGlobal(old) {
			return fmt.Errorf("refusing to replace modified OpenCode plugin at %s", path)
		}
		if remove {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("removing OpenCode plugin: %w", err)
			}
			return nil
		}
		if bytes.Equal(old, data) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking OpenCode plugin: %w", err)
	} else if remove {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating OpenCode plugin directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing OpenCode plugin: %w", err)
	}
	return nil
}
