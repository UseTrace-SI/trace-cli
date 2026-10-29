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

func TestFindWorktreeRoot(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "gitdir file"}[separate], func(t *testing.T) {
			root := t.TempDir()
			args := []string{"-C", root, "init", "-q"}
			if separate {
				args = append(args, "--separate-git-dir", filepath.Join(t.TempDir(), "gitdir"))
			}
			if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
				t.Fatalf("git init: %s: %v", out, err)
			}
			nested := filepath.Join(root, "src", "nested")
			if err := os.MkdirAll(nested, 0700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(root, alias); err != nil {
				t.Fatal(err)
			}
			for _, input := range []string{root, nested, filepath.Join(alias, "src")} {
				want, err := FindRoot(input)
				if err != nil {
					t.Fatal(err)
				}
				if got, err := FindWorktreeRoot(input); err != nil || got != want {
					t.Errorf("FindWorktreeRoot(%q) = %q, %v; want %q", input, got, err, want)
				}
			}
			if err := os.WriteFile(filepath.Join(nested, ".git"), []byte("invalid"), 0600); err != nil {
				t.Fatal(err)
			}
			if got, err := FindWorktreeRoot(nested); err == nil {
				t.Fatalf("invalid nested marker resolved to %q", got)
			}
		})
	}
	for _, input := range []string{"relative", t.TempDir(), "/nonexistent/trace-repo"} {
		if got, err := FindWorktreeRoot(input); err == nil {
			t.Errorf("unexpected root %q for %q", got, input)
		}
	}
}

func TestRelativePathResolvesAliasesAndMissingFiles(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(root, "escape")
	if err := os.Symlink(t.TempDir(), escape); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, want string
		wantError        bool
	}{
		{name: "existing alias", path: filepath.Join(alias, "file.txt"), want: "file.txt"},
		{name: "deleted alias", path: filepath.Join(alias, "deleted.txt"), want: "deleted.txt"},
		{name: "relative path", path: "file.txt", want: "file.txt"},
		{name: "outside path", path: filepath.Join(t.TempDir(), "file.txt"), wantError: true},
		{name: "symlink escape", path: filepath.Join(escape, "file.txt"), wantError: true},
		{name: "dangling symlink", path: filepath.Join(dangling, "file.txt"), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RelativePath(root, tc.path)
			if (err != nil) != tc.wantError || (!tc.wantError && got != tc.want) {
				t.Fatalf("RelativePath(%q) = %q, %v; want %q, error %t", tc.path, got, err, tc.want, tc.wantError)
			}
		})
	}
}
