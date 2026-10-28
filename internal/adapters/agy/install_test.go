package agy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallUninstallRestoresOriginal(t *testing.T) {
	for _, original := range []string{"", "{\n  \"other\": {\"SessionStart\": [{\"command\": \"echo keep\"}]}\n}\n"} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".agents"), 0700); err != nil {
			t.Fatal(err)
		}
		if original != "" {
			if err := os.WriteFile(hooksPath(root), []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := Install(root); err != nil {
			t.Fatal(err)
		}
		first, err := os.ReadFile(hooksPath(root))
		if err != nil {
			t.Fatal(err)
		}
		if err := Install(root); err != nil {
			t.Fatal(err)
		}
		second, err := os.ReadFile(hooksPath(root))
		if err != nil || string(first) != string(second) || strings.Count(string(second), " hook agy Stop") != 1 {
			t.Fatalf("install not idempotent: %v", err)
		}
		if err := Uninstall(root); err != nil {
			t.Fatal(err)
		}
		restored, err := os.ReadFile(hooksPath(root))
		if original == "" {
			if !os.IsNotExist(err) {
				t.Fatalf("expected removal, got %v", err)
			}
		} else if err != nil || string(restored) != original {
			t.Fatalf("restored=%q err=%v", restored, err)
		}
	}
}

func TestUninstallPreservesSubsequentUserEdit(t *testing.T) {
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(hooksPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatal(err)
	}
	settings["user"] = json.RawMessage(`{"Stop":[{"command":"echo keep"}]}`)
	b, err = json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooksPath(root), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(hooksPath(root))
	if err != nil || !strings.Contains(string(b), "echo keep") || strings.Contains(string(b), " hook agy ") {
		t.Fatalf("remaining hooks=%q err=%v", b, err)
	}
}
