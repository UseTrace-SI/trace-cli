package goose

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
)

const pluginDir = ".agents/plugins/trace"
const manifest = pluginDir + "/plugin.json"
const hooksPath = pluginDir + "/hooks/hooks.json"
const managed = "Trace hook bridge for goose"
const managedManifest = "{\n  \"name\": \"trace\",\n  \"version\": \"0.1.0\",\n  \"description\": \"" + managed + "\"\n}\n"

func isManagedManifest(b []byte) bool {
	var got, want map[string]any
	return json.Unmarshal(b, &got) == nil && json.Unmarshal([]byte(managedManifest), &want) == nil && reflect.DeepEqual(got, want)
}

var events = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"}

func Install(root string) error {
	path := filepath.Join(root, manifest)
	b, err := os.ReadFile(path)
	if err == nil && !isManagedManifest(b) {
		return fmt.Errorf("refusing to replace user goose plugin at %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading goose plugin: %w", err)
	}
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return fmt.Errorf("creating goose plugin: %w", err)
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("creating goose manifest: %w", err)
		}
		_, writeErr := f.WriteString(managedManifest)
		closeErr := f.Close()
		if writeErr != nil {
			return fmt.Errorf("writing goose plugin: %w", writeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("closing goose plugin: %w", closeErr)
		}
	}
	return hookconfig.Install(root, "goose", hooksPath, events, true)
}

func Uninstall(root string) error {
	if err := hookconfig.Uninstall(root, "goose", hooksPath, true); err != nil {
		return err
	}
	path := filepath.Join(root, manifest)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading goose plugin: %w", err)
	}
	if isManagedManifest(b) {
		return os.Remove(path)
	}
	return nil
}
