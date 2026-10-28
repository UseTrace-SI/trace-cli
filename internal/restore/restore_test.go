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

func TestLoadCreatedDirsRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), ".env.local")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, createdDirsPath(root)); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCreatedDirs(root); err == nil {
		t.Fatal("created directories record followed a symlink")
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

func TestApplyRemovesOnlyEmptyDirectoriesCreatedByTrace(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	userDir := filepath.Join(root, "user-empty")
	if err := os.Mkdir(userDir, 0700); err != nil {
		t.Fatal(err)
	}
	bs, err := blob.Open(filepath.Join(root, ".trace", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bs.PutBytes([]byte("restored"))
	if err != nil {
		t.Fatal(err)
	}
	target := manifest.Manifest{"user-empty/nested/deeper/file.txt": {Hash: hash, Mode: 0600}}
	if _, err := Apply(target, root, nil, bs); err != nil {
		t.Fatal(err)
	}
	marker, err := os.Stat(createdDirsPath(root))
	if err != nil || marker.Mode().Perm() != 0600 {
		t.Fatalf("created directories record mode: %v, %v", marker, err)
	}
	if _, err := Apply(manifest.Manifest{}, root, nil, bs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(userDir, "nested")); !os.IsNotExist(err) {
		t.Fatalf("Trace-created directory remains: %v", err)
	}
	if info, err := os.Stat(userDir); err != nil || !info.IsDir() {
		t.Fatalf("user directory was removed: %v", err)
	}
}

func TestApplyReplacesFileAndDirectoryAtSamePath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial string
		final   string
	}{
		{"file to directory", "entry", "entry/child.txt"},
		{"directory to file", "entry/nested/child.txt", "entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %s: %v", out, err)
			}
			bs, err := blob.Open(filepath.Join(root, ".trace", "objects"))
			if err != nil {
				t.Fatal(err)
			}
			initialHash, err := bs.PutBytes([]byte("old"))
			if err != nil {
				t.Fatal(err)
			}
			finalHash, err := bs.PutBytes([]byte("new"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(manifest.Manifest{tc.initial: {Hash: initialHash, Mode: 0600}}, root, nil, bs); err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(manifest.Manifest{tc.final: {Hash: finalHash, Mode: 0600}}, root, nil, bs); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tc.final)))
			if err != nil || string(content) != "new" {
				t.Fatalf("replacement content = %q, err = %v", content, err)
			}
			if tc.final == "entry" {
				if info, err := os.Stat(filepath.Join(root, "entry")); err != nil || !info.Mode().IsRegular() {
					t.Fatalf("entry was not replaced by a file: %v, %v", info, err)
				}
			} else if _, err := os.Stat(filepath.Join(root, "entry", "child.txt")); err != nil {
				t.Fatalf("entry was not replaced by a directory: %v", err)
			}
		})
	}
}

func TestApplyKeepsIgnoredFileUnderReplacedDirectory(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	ignore := []byte("entry/local.txt\n")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), ignore, 0600); err != nil {
		t.Fatal(err)
	}
	bs, err := blob.Open(filepath.Join(root, ".trace", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	ignoreHash, err := bs.PutBytes(ignore)
	if err != nil {
		t.Fatal(err)
	}
	contentHash, err := bs.PutBytes([]byte("tracked"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(manifest.Manifest{
		".gitignore":             {Hash: ignoreHash, Mode: 0600},
		"entry/nested/child.txt": {Hash: contentHash, Mode: 0600},
	}, root, nil, bs); err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(root, "entry", "local.txt")
	if err := os.WriteFile(ignored, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(manifest.Manifest{
		".gitignore": {Hash: ignoreHash, Mode: 0600},
		"entry":      {Hash: contentHash, Mode: 0600},
	}, root, nil, bs); err == nil {
		t.Fatal("restore replaced directory containing an ignored file")
	}
	if content, err := os.ReadFile(ignored); err != nil || string(content) != "keep me" {
		t.Fatalf("ignored file was removed: %q, %v", content, err)
	}
}
