package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGlobalSetupPreservesUserHooks(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"my-hook"}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := setupAgent(home, "codex", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "my-hook") || !strings.Contains(string(b), " hook codex Stop") {
		t.Fatalf("installed hooks: %s, %v", b, err)
	}
	if err := setupAgent(home, "codex", true); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil || !sameJSON(b, []byte(original)) {
		t.Fatalf("restored hooks: %s, %v", b, err)
	}
}

func TestOpenClaudeGlobalSetupRespectsConfigDirectory(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(t.TempDir(), "custom-config")
	t.Setenv("OPENCLAUDE_CONFIG_DIR", config)
	if err := setupAgent(home, "openclaude", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "settings.json")
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), " hook openclaude Stop") {
		t.Fatalf("hooks: %s, %v", b, err)
	}
	if err := setupAgent(home, "openclaude", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("hooks remain: %v", err)
	}
}

func TestClaudeGlobalSetupRespectsConfigDirectory(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(t.TempDir(), "custom-claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "settings.json")
	original := []byte(`{"model":"custom","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo keep"}]}]}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	if err := setupAgent(home, "claude-code", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "echo keep") || !strings.Contains(string(b), " hook claude-code Stop") {
		t.Fatalf("installed: %s %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("default settings changed: %v", err)
	}
	if err := setupAgent(home, "claude-code", true); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil || !sameJSON(b, original) {
		t.Fatalf("removed: %s %v", b, err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "relative")
	if err := setupAgent(home, "claude-code", false); err == nil {
		t.Fatal("accepted relative config directory")
	}
}

func TestCodexSetupJSONReportsTrustReview(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	output := new(bytes.Buffer)
	jsonOutput := true
	cmd := setupCommand(&jsonOutput)
	cmd.SetOut(output)
	cmd.SetArgs([]string{"--agent", "codex"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Enabled    bool   `json:"enabled"`
		Additional string `json:"additional_setup"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Enabled || !strings.Contains(result.Additional, "/hooks") {
		t.Fatalf("unexpected setup status: %s", output.String())
	}
}

func TestGeminiSetupReportsHookEnablement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GEMINI_CLI_HOME", home)
	output := new(bytes.Buffer)
	jsonOutput := true
	cmd := setupCommand(&jsonOutput)
	cmd.SetOut(output)
	cmd.SetArgs([]string{"--agent", "gemini"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Enabled    bool   `json:"enabled"`
		Additional string `json:"additional_setup"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Enabled || !strings.Contains(result.Additional, "hooks.enabled=true") {
		t.Fatalf("unexpected setup status: %s", output.String())
	}
	data, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Hooks["enabled"]; ok {
		t.Fatal("setup changed user hook enablement")
	}
}

func TestQwenSetupReportsHookRequirement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	output := new(bytes.Buffer)
	jsonOutput := true
	cmd := setupCommand(&jsonOutput)
	cmd.SetOut(output)
	cmd.SetArgs([]string{"--agent", "qwen"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Enabled    bool   `json:"enabled"`
		Additional string `json:"additional_setup"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Enabled || !strings.Contains(result.Additional, "0.0.14") {
		t.Fatalf("unexpected setup status: %s", output.String())
	}
}

func TestWindsurfSetupJSONReportsRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	output := new(bytes.Buffer)
	jsonOutput := true
	cmd := setupCommand(&jsonOutput)
	cmd.SetOut(output)
	cmd.SetArgs([]string{"--agent", "windsurf"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Enabled    bool   `json:"enabled"`
		Additional string `json:"additional_setup"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Enabled || !strings.Contains(result.Additional, "Restart") {
		t.Fatalf("unexpected setup status: %s", output.String())
	}
	for _, path := range []string{".codeium/windsurf/hooks.json", ".codeium/hooks.json"} {
		if _, err := os.Stat(filepath.Join(home, path)); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
}

func TestPoolsideSetupReportsVersionAndRestart(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	pool := filepath.Join(bin, "pool")
	if err := os.WriteFile(pool, []byte("#!/bin/sh\nprintf '1.0.14\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, jsonOutput := range []bool{false, true} {
		output := new(bytes.Buffer)
		cmd := setupCommand(&jsonOutput)
		cmd.SetOut(output)
		cmd.SetArgs([]string{"--agent", "poolside"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "1.0.14") || !strings.Contains(output.String(), "1.0.16") || !strings.Contains(output.String(), "restart") {
			t.Fatalf("missing setup guidance: %s", output.String())
		}
		if jsonOutput {
			var result struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Enabled {
				t.Fatalf("unexpected status: %s, %v", output.String(), err)
			}
		}
	}
}

func TestPoolsideSetupVersionParsing(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, tt := range []struct {
		version string
		old     bool
	}{
		{"1.0.14", true}, {"v1.0.16-rc.1", true}, {"1.0.16", false},
		{"pool 1.1.0", false}, {"2.0.0", false}, {"unknown", false},
	} {
		t.Run(tt.version, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(bin, "pool"), []byte("#!/bin/sh\nprintf '%s\\n' '"+tt.version+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(poolsideSetupNotice(), "too old"); got != tt.old {
				t.Fatalf("version %s: got old=%t", tt.version, got)
			}
		})
	}
}

func TestAuggieGlobalSetupPreservesHooks(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".augment", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	original := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"custom"}]}]},"other":true}`
	if err := os.WriteFile(p, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := setupAgent(home, "auggie", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "custom") || !strings.Contains(string(b), "hook auggie PromptSubmit") {
		t.Fatalf("installed: %s, %v", b, err)
	}
	if err := setupAgent(home, "auggie", true); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(p)
	if err != nil || !sameJSON(b, []byte(original)) {
		t.Fatalf("restored: %s, %v", b, err)
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
