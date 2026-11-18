package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCompanionLearnedContextFailsExplicitlyWhenBrainIsUnavailable(t *testing.T) {
	root := competitiveRepo(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	jsonOutput := true
	command := contextCommand(&root, &jsonOutput)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"car", "--learned-model", "test:1"})
	if err := command.Execute(); err == nil || output.Len() != 0 {
		t.Fatalf("explicit lookup returned an offline substitute: %s %v", &output, err)
	}
}
func TestCompanionLearnedContextInvalidFlagsDoNotInitializeARepository(t *testing.T) {
	root := t.TempDir()
	jsonOutput := false
	for _, args := range [][]string{{"car", "--learned-endpoint", "http://localhost"}, {"car", "--learned-model", "test:1", "--learned-endpoint", "https://example.com"}, {"--learned-model", "test:1"}} {
		command := contextCommand(&root, &jsonOutput)
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		command.SetArgs(args)
		if err := command.Execute(); err == nil {
			t.Fatal("invalid learned config accepted")
		}
		if _, err := os.Stat(filepath.Join(root, ".trace")); !os.IsNotExist(err) {
			t.Fatal("invalid lookup created project state", err)
		}
	}
}
