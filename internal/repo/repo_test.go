package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFindRoot(t *testing.T) {
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	nested := filepath.Join(root, "src", "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		wantError  bool
	}{
		{"root", root, false}, {"nested", nested, false}, {"outside", t.TempDir(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FindRoot(tc.path)
			if (err != nil) != tc.wantError {
				t.Fatalf("root=%q error=%v", got, err)
			}
			if !tc.wantError && got != canonical {
				t.Fatalf("root=%q want=%q", got, canonical)
			}
		})
	}
	if TraceDir(root) != filepath.Join(root, ".trace") {
		t.Fatal("wrong trace directory")
	}
}
