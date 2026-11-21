package gitx

import (
	"errors"
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
	// Shell hooks keep their name and gain the block after the shebang. The
	// block runs this test binary as the Trace executable (see TestMain).
	cmd := exec.Command("sh", p)
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

func hookGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return string(out)
}

func writeHookFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
}

// Hook managers such as overcommit choose what to run from "$0". Renaming the
// user's hook to post-commit.pre-trace made them look up the wrong hook.
func TestInstallHookKeepsShellHookName(t *testing.T) {
	for _, shebang := range []string{"#!/bin/sh", "#!/usr/bin/env bash", "#!/bin/sh -e"} {
		t.Run(shebang, func(t *testing.T) {
			root := t.TempDir()
			hookGit(t, root, "init", "-q")
			if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "hook-name")
			p, err := GitPath(root, "hooks/post-commit")
			if err != nil {
				t.Fatal(err)
			}
			original := shebang + "\nbasename \"$0\" > '" + marker + "'\n"
			writeHookFiles(t, filepath.Dir(p), map[string]string{"post-commit": original})
			if err := InstallHook(root); err != nil {
				t.Fatal(err)
			}
			hookGit(t, root, "commit", "-q", "--allow-empty", "-m", "first")
			if b, err := os.ReadFile(marker); err != nil || strings.TrimSpace(string(b)) != "post-commit" {
				t.Fatalf("user hook saw its name as %q (%v); want post-commit", b, err)
			}
			if _, err := os.Stat(p + ".pre-trace"); !os.IsNotExist(err) {
				t.Fatalf("shell hook was renamed: %v", err)
			}
			if err := UninstallHook(root); err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(p); err != nil || string(b) != original {
				t.Fatalf("uninstall changed the user hook: %q, %v", b, err)
			}
		})
	}
}

// A core.hooksPath inside the worktree holds tracked team hooks or files a
// hook manager generates (husky). Trace must not edit or rename them.
func TestInstallHookLeavesWorktreeHooksPathUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, hooksPath string
		files           map[string]string
		track           []string
	}{
		{"tracked hooks directory", ".githooks", map[string]string{".githooks/post-commit": "#!/bin/sh\necho team hook\n"}, []string{".githooks/post-commit"}},
		{"husky", ".husky/_", map[string]string{
			".husky/_/h":           "#!/usr/bin/env sh\nn=$(basename \"$0\")\ns=$(dirname \"$(dirname \"$0\")\")/$n\n[ ! -f \"$s\" ] && exit 0\nsh -e \"$s\" \"$@\"\n",
			".husky/_/post-commit": "#!/usr/bin/env sh\n. \"$(dirname \"$0\")/h\"\n",
			".husky/_/.gitignore":  "*\n",
			".husky/post-commit":   "printf ran > ran-marker\n",
		}, []string{".husky/post-commit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			hookGit(t, root, "init", "-q")
			writeHookFiles(t, root, tc.files)
			writeHookFiles(t, root, map[string]string{".gitignore": ".trace/\nran-marker\n"})
			hookGit(t, root, append([]string{"add", ".gitignore"}, tc.track...)...)
			hookGit(t, root, "commit", "-q", "-m", "hooks")
			hookGit(t, root, "config", "core.hooksPath", tc.hooksPath)
			if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
				t.Fatal(err)
			}
			err := InstallHook(root)
			var manual *ManualHookError
			if !errors.As(err, &manual) || len(manual.Hooks) != 2 || !strings.Contains(manual.Hooks[0].Block, begin) {
				t.Fatalf("InstallHook = %v; want a manual setup notice for both hooks", err)
			}
			for name, body := range tc.files {
				if b, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(b) != body {
					t.Errorf("%s changed: %q, %v", name, b, err)
				}
			}
			if status := hookGit(t, root, "status", "--porcelain", "--ignored"); strings.Contains(status, "pre-trace") || strings.Contains(status, " M ") {
				t.Fatalf("hook files changed:\n%s", status)
			}
			if tc.name == "husky" {
				hookGit(t, root, "commit", "-q", "--allow-empty", "-m", "second")
				if _, err := os.Stat(filepath.Join(root, "ran-marker")); err != nil {
					t.Fatal("husky no longer runs the user's post-commit hook")
				}
			}
			if err := UninstallHook(root); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstallHookChainsNonShellHook(t *testing.T) {
	root := t.TempDir()
	hookGit(t, root, "init", "-q")
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	p, err := GitPath(root, "hooks/post-commit")
	if err != nil {
		t.Fatal(err)
	}
	original := "#!/usr/bin/env python3\nprint('hook')\n"
	writeHookFiles(t, filepath.Dir(p), map[string]string{"post-commit": original})
	if err := InstallHook(root); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(p + ".pre-trace"); err != nil || string(b) != original {
		t.Fatalf("non-shell hook was not preserved for chaining: %q, %v", b, err)
	}
	installed, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(installed), postCommitHook.chain) || !strings.Contains(string(installed), begin) {
		t.Fatalf("chained hook = %q, %v", installed, err)
	}
	if err := UninstallHook(root); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(p); err != nil || string(b) != original {
		t.Fatalf("uninstall did not restore the original: %q, %v", b, err)
	}
}

// Git passes post-rewrite its rewrite type and "old new" pairs on stdin. The
// Trace block must leave both intact for the user's own hook.
func TestInstallHookKeepsPostRewriteInput(t *testing.T) {
	for _, tc := range []struct {
		name, hook string
		needs      string
	}{
		{"shell hook", "#!/bin/sh\nprintf '%s\\n' \"$1\" > \"$MARKER\"\ncat >> \"$MARKER\"\n", ""},
		{"other interpreter", "#!/usr/bin/env python3\nimport os, sys\nopen(os.environ['MARKER'], 'w').write(sys.argv[1] + '\\n' + sys.stdin.read())\n", "python3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needs != "" {
				if _, err := exec.LookPath(tc.needs); err != nil {
					t.Skipf("%s not installed", tc.needs)
				}
			}
			root := t.TempDir()
			hookGit(t, root, "init", "-q")
			if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
				t.Fatal(err)
			}
			p, err := GitPath(root, "hooks/post-rewrite")
			if err != nil {
				t.Fatal(err)
			}
			writeHookFiles(t, filepath.Dir(p), map[string]string{"post-rewrite": tc.hook})
			if err := InstallHook(root); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "rewrites")
			t.Setenv("MARKER", marker)
			hookGit(t, root, "commit", "-q", "--allow-empty", "-m", "first")
			before := strings.TrimSpace(hookGit(t, root, "rev-parse", "HEAD"))
			hookGit(t, root, "commit", "-q", "--amend", "--allow-empty", "-m", "amended")
			after := strings.TrimSpace(hookGit(t, root, "rev-parse", "HEAD"))
			b, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			if want := "amend\n" + before + " " + after + "\n"; string(b) != want {
				t.Fatalf("user hook received %q; want %q", b, want)
			}
			if err := UninstallHook(root); err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(p); err != nil || string(b) != tc.hook {
				t.Fatalf("uninstall did not restore the user hook: %q, %v", b, err)
			}
		})
	}
}

func TestInstallHookCreatesBothHooks(t *testing.T) {
	root := t.TempDir()
	hookGit(t, root, "init", "-q")
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := InstallHook(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"post-commit", "post-rewrite"} {
		p, err := GitPath(root, "hooks/"+name)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm()&0100 == 0 {
			t.Fatalf("%s hook missing or not executable: %v", name, err)
		}
	}
	hookGit(t, root, "commit", "-q", "--allow-empty", "-m", "first")
	hookGit(t, root, "commit", "-q", "--amend", "--allow-empty", "-m", "amended")
	if err := UninstallHook(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"post-commit", "post-rewrite"} {
		p, _ := GitPath(root, "hooks/"+name)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s hook left after uninstall: %v", name, err)
		}
	}
}
