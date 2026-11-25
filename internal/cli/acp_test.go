package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestACPAgentName(t *testing.T) {
	for in, want := range map[string]string{
		"claude-agent-acp":            "claude-agent-acp",
		"/usr/local/bin/codex-acp":    "codex-acp",
		"C:/tools/Gemini_CLI.exe":     "gemini-cli",
		"npx":                         "npx",
		"!!!":                         "agent",
		"/opt/agents/My Agent.v2.cmd": "my-agent-v2",
	} {
		if got := acpAgentName(in); got != want {
			t.Errorf("acpAgentName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestACPNameFlagIsSanitized(t *testing.T) {
	for in, want := range map[string]string{"Zed_Claude": "zed-claude", "zed.claude": "zed-claude", "ok-1": "ok-1", "@@": "agent"} {
		if got := acpNameSafe(in); got != want {
			t.Errorf("acpNameSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestACPSinkSkipsUninitializedRepositories(t *testing.T) {
	t.Setenv("TRACE_HOME", t.TempDir())
	plain := t.TempDir()
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	var logs []string
	s := &acpSink{logf: func(f string, a ...any) { logs = append(logs, f) }, roots: map[string]string{}}
	s.emit(event.Event{Kind: event.SessionStart, Agent: "acp:x", AgentSessionID: "s", Worktree: plain})
	s.emit(event.Event{Kind: event.SessionStart, Agent: "acp:x", AgentSessionID: "s", Worktree: repo})
	if len(logs) != 2 || !strings.Contains(logs[0], "not in a Git repository") || !strings.Contains(logs[1], "not initialized") {
		t.Fatalf("logs = %q", logs)
	}
	if _, err := os.Stat(filepath.Join(repo, ".trace")); !os.IsNotExist(err) {
		t.Fatalf("sink created .trace in an uninitialized repository: %v", err)
	}
}
