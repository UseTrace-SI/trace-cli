package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigHome(t *testing.T) {
	home := t.TempDir()
	selected := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, selected)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, value, want string
		fails             bool
	}{
		{"default", "", filepath.Join(home, ".codex"), false},
		{"selected", selected, selected, false},
		{"relative selected", relative, selected, false},
		{"relative missing", "trace-nonexistent-codex-home", "", true},
		{"missing", filepath.Join(home, "missing"), "", true},
		{"file", file, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CODEX_HOME", tc.value)
			got, err := ConfigHome(home)
			if (err != nil) != tc.fails || (!tc.fails && got != tc.want) {
				t.Fatalf("ConfigHome() = %q, %v", got, err)
			}
		})
	}
}
