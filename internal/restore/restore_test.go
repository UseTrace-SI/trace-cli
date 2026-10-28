package restore

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

func TestSafePathRejectsUnsafeTargets(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../escape", ".env.local", ".git/config", "link/child.go"} {
		if _, err := safePath(root, rel); err == nil {
			t.Fatalf("accepted unsafe path %q", rel)
		}
	}
}

func TestApplyRestoresBytesAndMode(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("remove me"), 0600); err != nil {
		t.Fatal(err)
	}
	bs, err := blob.Open(filepath.Join(root, ".trace", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bs.PutBytes([]byte{0, 1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	target := manifest.Manifest{"nested/new.bin": {Hash: hash, Mode: 0755}}
	changed, err := Apply(target, root, nil, bs)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 {
		t.Fatalf("changed paths: %v", changed)
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old file remains: %v", err)
	}
	p := filepath.Join(root, "nested", "new.bin")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string([]byte{0, 1, 2, 3}) {
		t.Fatalf("restored bytes: %v", b)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("mode: %o", info.Mode().Perm())
	}
}
