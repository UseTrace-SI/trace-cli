package omp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallUninstall(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, relative)
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `const agentName = "omp"`) || !strings.Contains(string(b), root) {
		t.Fatalf("installed Oh My Pi extension = %s, %v", b, err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Oh My Pi extension remains: %v", err)
	}
}
