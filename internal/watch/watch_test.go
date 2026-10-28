package watch

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestAddDirsSkipsGitIgnoredTrees(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"src", "node_modules/dep", "docs"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "dep", "index.js"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := addDirs(w, root); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range w.WatchList() {
		seen[p] = true
	}
	if !seen[root] || !seen[filepath.Join(root, "src")] {
		t.Fatalf("missing watched directories: %v", w.WatchList())
	}
	if seen[filepath.Join(root, "node_modules")] || seen[filepath.Join(root, "node_modules", "dep")] {
		t.Fatalf("watched ignored tree: %v", w.WatchList())
	}
	if seen[filepath.Join(root, ".git")] {
		t.Fatalf("watched .git: %v", w.WatchList())
	}
}

func TestAddDirsExcludesPrivatePaths(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"src", ".trace", ".aws", ".ssh"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := addDirs(w, root); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range w.WatchList() {
		seen[p] = true
	}
	for _, tc := range []struct {
		name string
		want bool
	}{{"", true}, {"src", true}, {".trace", false}, {".aws", false}, {".ssh", false}} {
		p := filepath.Join(root, tc.name)
		if seen[p] != tc.want {
			t.Fatalf("watch %s=%t want %t", tc.name, seen[p], tc.want)
		}
	}
}
