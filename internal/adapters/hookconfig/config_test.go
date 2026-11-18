package hookconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstallUninstall(t *testing.T) {
	for _, tc := range []struct {
		agent, path string
		grouped     bool
	}{
		{"cursor", ".cursor/hooks.json", false},
		{"gemini", ".gemini/settings.json", true},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(root, tc.path)
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte("{\n  \"unrelated\": true\n}\n")
			if err := os.WriteFile(p, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := Install(root, tc.agent, tc.path, []string{"SessionStart"}, tc.grouped); err != nil {
				t.Fatal(err)
			}
			if err := Install(root, tc.agent, tc.path, []string{"SessionStart"}, tc.grouped); err != nil {
				t.Fatal(err)
			}
			installed, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(installed), " hook "+tc.agent+" SessionStart") != 1 {
				t.Fatalf("duplicate hook: %s", installed)
			}
			if err := Uninstall(root, tc.agent, tc.path, tc.grouped); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			var result, expected map[string]any
			if err := json.Unmarshal(got, &result); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(original, &expected); err != nil {
				t.Fatal(err)
			}
			if tc.agent == "cursor" {
				expected["version"] = float64(1)
			}
			if !reflect.DeepEqual(result, expected) {
				t.Fatalf("settings changed: %q", got)
			}
		})
	}
}

func TestAtomicWrite(t *testing.T) {
	for _, tc := range []struct {
		name        string
		initial     []byte
		mode        os.FileMode
		wantMode    os.FileMode
		wantErr     bool
		targetIsDir bool
	}{
		{"new file", nil, 0, 0600, false, false},
		{"existing private file", []byte("old"), 0600, 0600, false, false},
		{"preserves shared config mode", []byte("old"), 0644, 0644, false, false},
		{"failed replace leaves target intact", nil, 0, 0, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.json")
			if tc.targetIsDir {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if tc.initial != nil {
				if err := os.WriteFile(path, tc.initial, tc.mode); err != nil {
					t.Fatal(err)
				}
			}
			err := atomicWrite(path, []byte("new content"))
			if (err != nil) != tc.wantErr {
				t.Fatalf("atomicWrite error = %v, wantErr %t", err, tc.wantErr)
			}
			if !tc.wantErr {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "new content" {
					t.Fatalf("content = %q, %v", data, err)
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != tc.wantMode {
					t.Fatalf("mode = %v, want %v (%v)", info, tc.wantMode, err)
				}
			} else {
				info, err := os.Stat(path)
				if err != nil || !info.IsDir() {
					t.Fatalf("target was changed by a failed write: %v, %v", info, err)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".trace-hook-") {
					t.Fatalf("temporary file was not cleaned up: %s", entry.Name())
				}
			}
		})
	}
}

func TestInstallPreservesConfigMode(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{\n  \"unrelated\": true\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Install(root, "gemini", ".gemini/settings.json", []string{"SessionStart"}, true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("install changed config mode: %v, %v", info, err)
	}
}

func TestRejectsUnexpectedHookConfiguration(t *testing.T) {
	for _, original := range []string{`{"hooks":"custom"}`, `{"hooks":{"Stop":{"custom":true}}}`} {
		t.Run(original, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(root, "settings.json")
			if err := os.WriteFile(p, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if err := Install(root, "auggie", "settings.json", []string{"Stop"}, true); err == nil {
				t.Fatal("expected invalid hook configuration error")
			}
			b, err := os.ReadFile(p)
			if err != nil || string(b) != original {
				t.Fatalf("configuration changed: %s, %v", b, err)
			}
		})
	}
}

func TestReinstallPreservesEditsAndResetsBackup(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "settings.json")
	if err := os.WriteFile(p, []byte(`{"other":"original"}`), 0600); err != nil {
		t.Fatal(err)
	}
	install := func() {
		t.Helper()
		if err := Install(root, "auggie", "settings.json", []string{"Stop"}, true); err != nil {
			t.Fatal(err)
		}
	}
	uninstall := func() {
		t.Helper()
		if err := Uninstall(root, "auggie", "settings.json", true); err != nil {
			t.Fatal(err)
		}
	}
	install()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	data["other"] = "edited"
	b, err = json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	install()
	uninstall()
	b, err = os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"edited"`) || strings.Contains(string(b), "hook auggie Stop") {
		t.Fatalf("lost user edits or retained hook: %s", b)
	}
	install()
	uninstall()
	b, err = os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"edited"`) {
		t.Fatalf("restored stale backup: %s", b)
	}
}

func TestInstallDoesNotCopyUserConfiguration(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "settings.json")
	if err := os.WriteFile(p, []byte(`{"apiKey":"private-value","hooks":{"Stop":[{"hooks":[]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root, "auggie", "settings.json", []string{"Stop"}, true); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".trace"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected config backup: %v", entries)
	}
	if err := Uninstall(root, "auggie", "settings.json", true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	if data["apiKey"] != "private-value" {
		t.Fatalf("user value lost: %s", b)
	}
	hooks := data["hooks"].(map[string]any)["Stop"].([]any)
	if len(hooks) != 1 {
		t.Fatalf("user hook group lost: %s", b)
	}
}

func TestPreservesUnrelatedHookWithSimilarArguments(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "settings.json")
	original := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/usr/bin/other hook auggie Stop"},{"type":"command","command":"echo hook auggie Stop"}]}]}}`
	if err := os.WriteFile(p, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root, "auggie", "settings.json", []string{"Stop"}, true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "/usr/bin/other hook auggie Stop") || !strings.Contains(string(b), "echo hook auggie Stop") || strings.Count(string(b), " hook auggie Stop") != 3 {
		t.Fatalf("install modified user hook: %s %v", b, err)
	}
	if err := Uninstall(root, "auggie", "settings.json", true); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(p)
	var got, want any
	if err != nil || json.Unmarshal(b, &got) != nil || json.Unmarshal([]byte(original), &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("uninstall modified user hook: %s %v", b, err)
	}
}
