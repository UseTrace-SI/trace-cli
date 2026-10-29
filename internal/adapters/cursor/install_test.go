package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalInstallPreservesOtherHooks(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	original := `{"version":1,"other":true,"hooks":{"stop":[{"command":"my-user-hook"}]}}`
	if err := os.WriteFile(p, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := InstallGlobal(home); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "my-user-hook") || strings.Count(string(b), " hook cursor-global stop") != 1 {
		t.Fatalf("installed config: %s, %v", b, err)
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
		t.Fatalf("user config changed: %s", b)
	}
}

func TestHasProjectHook(t *testing.T) {
	root := t.TempDir()
	if HasProjectHook(root, "stop") {
		t.Fatal("missing project hook detected")
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if !HasProjectHook(root, "stop") || HasProjectHook(root, "unknown") {
		t.Fatal("wrong project hook detection")
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if HasProjectHook(root, "stop") {
		t.Fatal("removed project hook detected")
	}
}

func TestGlobalInstallPreservesUnknownVersion(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	original := `{"version":2,"hooks":{}}`
	if err := os.WriteFile(p, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(home); err == nil {
		t.Fatal("accepted unsupported config version")
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != original {
		t.Fatalf("config changed: %q, %v", b, err)
	}
}
