package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExplicitRepoPathResolvesGitRoot(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("init git repo: %v: %s", err, out)
	}
	nested := filepath.Join(root, "src", "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{root, nested} {
		t.Run(filepath.Base(input), func(t *testing.T) {
			got, err := rootFromFlag(input)
			if err != nil {
				t.Fatal(err)
			}
			if got != canonicalRoot {
				t.Fatalf("rootFromFlag(%q) = %q, want %q", input, got, canonicalRoot)
			}
		})
	}
	if err := os.Mkdir(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".trace", "trace.db"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := initializedRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != canonicalRoot {
		t.Fatalf("initializedRoot(%q) = %q, want %q", nested, got, canonicalRoot)
	}
}
