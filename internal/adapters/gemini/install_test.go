package gemini

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalInstallUsesOverrideAndPreservesSettings(t *testing.T) {
	home := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("GEMINI_CLI_HOME", configHome)
	p := filepath.Join(configHome, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	original := `{"other":true,"hooks":{"AfterAgent":[{"hooks":[{"type":"command","command":"user-hook"}]}]}}`
	if err := os.WriteFile(p, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := InstallGlobal(home); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "user-hook") || strings.Count(string(b), " hook gemini-global AfterAgent") != 1 {
		t.Fatalf("settings: %s, %v", b, err)
	}
	if err := UninstallGlobal(home); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if json.Unmarshal(b, &got) != nil || json.Unmarshal([]byte(original), &want) != nil {
		t.Fatal("invalid JSON")
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("settings changed: %s", b)
	}
	t.Setenv("GEMINI_CLI_HOME", "relative")
	if err := InstallGlobal(home); err == nil {
		t.Fatal("accepted relative config home")
	}
}

func TestHasProjectHook(t *testing.T) {
	root := t.TempDir()
	if HasProjectHook(root, "AfterAgent") {
		t.Fatal("detected missing hook")
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if !HasProjectHook(root, "AfterAgent") || HasProjectHook(root, "unknown") {
		t.Fatal("wrong hook detection")
	}
	path := filepath.Join(root, ".gemini", "settings.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	hooks := cfg["hooks"].(map[string]any)
	hooks["enabled"] = true
	hooks["disabled"] = []string{"user-hook"}
	b, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if !HasProjectHook(root, "AfterAgent") {
		t.Fatal("hook detection failed with Gemini hook settings")
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if HasProjectHook(root, "AfterAgent") {
		t.Fatal("detected removed hook")
	}
}
