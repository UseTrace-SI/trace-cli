package hookconfig

import (
	"os"
	"path/filepath"
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
			if string(got) != string(original) {
				t.Fatalf("settings changed: %q", got)
			}
		})
	}
}
