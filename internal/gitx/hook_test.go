package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
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
	if !strings.Contains(string(installed), "| "+hookcmd.Command("git", "git_commit")+" >/dev/null") {
		t.Fatalf("post-commit hook does not use an absolute command: %s", installed)
	}
	if strings.Contains(string(installed), "| trace hook") {
		t.Fatalf("post-commit hook kept a PATH-dependent command: %s", installed)
	}
	// The preserved hook runs before Trace; exercise it directly so the test
	// binary is not invoked as the Trace executable.
	cmd := exec.Command("sh", p+".pre-trace")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running original hook: %s: %v", out, err)
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

func TestInstallHookMigratesBareCommand(t *testing.T) {
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
	legacy := "#!/bin/sh\nif [ -x \"$0.pre-trace\" ]; then \"$0.pre-trace\" \"$@\"; fi\n" + begin +
		"sha=$(git rev-parse HEAD 2>/dev/null) || exit 0\n" +
		"printf '{\"git_sha\":\"%s\"}\\n' \"$sha\" | trace hook git git_commit >/dev/null 2>&1 || true\n" + end
	if err := os.WriteFile(p+".pre-trace", original, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(legacy), 0755); err != nil {
		t.Fatal(err)
	}
	if err := InstallHook(root); err != nil {
		t.Fatal(err)
	}
	migrated, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(migrated), "| trace hook") {
		t.Fatalf("bare command was not migrated: %s", migrated)
	}
	if !strings.Contains(string(migrated), "| "+hookcmd.Command("git", "git_commit")) {
		t.Fatalf("absolute command missing: %s", migrated)
	}
	if strings.Count(string(migrated), begin) != 1 {
		t.Fatalf("migration duplicated the block: %s", migrated)
	}
}
