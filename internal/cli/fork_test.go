package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/id"
)

func TestAgentLaunchCommand(t *testing.T) {
	for _, tc := range []struct {
		agent string
		name  string
		args  []string
	}{
		{"claude-code", "claude", []string{"context"}},
		{"codex", "codex", []string{"context"}},
		{"agy", "agy", []string{"--prompt-interactive", "context"}},
		{"cursor", "agent", []string{"context"}},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			name, args, err := agentLaunchCommand(tc.agent, "context")
			if err != nil || name != tc.name || !reflect.DeepEqual(args, tc.args) {
				t.Fatalf("name=%q args=%v err=%v", name, args, err)
			}
		})
	}
	if _, _, err := agentLaunchCommand("unsupported", "context"); err == nil {
		t.Fatal("unsupported agent accepted")
	}
}

func TestForkRemovesWorktreeOnSetupFailure(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	head := git("rev-parse", "HEAD")
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	var manifestHash string
	if err := e.DB.QueryRow("SELECT manifest_hash FROM keyframes WHERE seq=0").Scan(&manifestHash); err != nil {
		t.Fatal(err)
	}
	keyID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO keyframes(id,seq,manifest_hash,git_head,tag,created_at) VALUES(?,1,?,?,'turn',?)", keyID, manifestHash, head, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	forkRoot := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-fork-1")
	repoPath, jsonOutput := root, false
	cmd := forkCommand(&repoPath, &jsonOutput)
	// The test binary cannot run the nested `init` command, so this fails after
	// the worktree has been created and must roll it back.
	cmd.SetArgs([]string{"1", "--agent", "bogus", "--no-launch"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected fork setup to fail")
	}
	if _, err := os.Stat(forkRoot); !os.IsNotExist(err) {
		t.Fatalf("orphaned fork directory: %v", err)
	}
	if list := git("worktree", "list", "--porcelain"); strings.Contains(list, forkRoot) {
		t.Fatalf("orphaned git worktree: %s", list)
	}
}
