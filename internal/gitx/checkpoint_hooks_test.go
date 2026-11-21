package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// failingScript writes an executable that records that it ran, then fails.
func failingScript(t *testing.T, path, marker string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"$0\" >> '"+marker+"'\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
}

// Checkpoints commit to Trace's hidden branch from a temporary worktree. The
// project's hooks and signing setup must neither block them nor run for them
// (a post-commit hook that pushes HEAD would publish the branch).
func TestCheckpointIgnoresUserHooksAndSigning(t *testing.T) {
	hook := func(name string) func(t *testing.T, root, marker string) {
		return func(t *testing.T, root, marker string) {
			failingScript(t, filepath.Join(root, ".git", "hooks", name), marker)
		}
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root, marker string)
	}{
		{"pre-commit", hook("pre-commit")},
		{"commit-msg", hook("commit-msg")},
		{"post-commit", hook("post-commit")},
		{"post-checkout", hook("post-checkout")},
		{"reference-transaction", hook("reference-transaction")},
		{"commit signing", func(t *testing.T, root, marker string) {
			program := filepath.Join(t.TempDir(), "gpg")
			failingScript(t, program, marker)
			gitIn(t, root, "config", "commit.gpgsign", "true")
			gitIn(t, root, "config", "gpg.program", program)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			root := t.TempDir()
			gitIn(t, root, "init", "-q")
			gitIn(t, root, "config", "user.email", "test@example.com")
			gitIn(t, root, "config", "user.name", "Test")
			if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".trace", "keep.txt"), []byte("history"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := EnableGitNative(root); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "ran")
			tc.setup(t, root, marker)
			if err := Checkpoint(root); err != nil {
				t.Errorf("checkpoint failed: %v", err)
			}
			if b, err := os.ReadFile(marker); err == nil {
				t.Errorf("checkpoint ran the project's %s: %s", tc.name, b)
			}
			// The branch holds the initial commit and the checkpoint.
			if n := gitIn(t, root, "rev-list", "--count", CheckpointBranch); n != "2" {
				t.Errorf("checkpoint branch has %s commits, want 2", n)
			}
		})
	}
}
