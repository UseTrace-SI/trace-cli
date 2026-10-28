package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChainPostCommit(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	p, err := GitPath(root, "hooks/post-commit")
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("#!/bin/sh\nprintf original > '" + filepath.Join(root, "ran") + "'\n")
	if err := os.WriteFile(p, original, 0755); err != nil {
		t.Fatal(err)
	}
	if err := InstallHook(root); err != nil {
		t.Fatal(err)
	}
	if err := InstallHook(root); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(installed), begin) != 1 {
		t.Fatal("hook duplicated")
	}
	cmd := exec.Command("sh", p)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running hook: %s: %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(root, "ran")); err != nil {
		t.Fatal("original hook did not run")
	}
	if err := UninstallHook(root); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(original) {
		t.Fatalf("original hook changed: %q", restored)
	}
}
