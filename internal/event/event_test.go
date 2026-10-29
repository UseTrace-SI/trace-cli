package event

import (
	"strings"
	"testing"
)

func TestSanitizedDropsSecrets(t *testing.T) {
	e := Event{Kind: ToolEdit, Prompt: "ghp_abcdefghijklmnopqrstuvwxyz", Paths: []string{"src/main.go", ".env.local", ".aws/credentials"}, TranscriptPath: ".ssh/id_rsa", Raw: []byte(`{"secret":"raw"}`)}
	clean := e.Sanitized()
	if len(clean.Paths) != 1 || clean.Paths[0] != "src/main.go" {
		t.Fatalf("unsafe paths retained: %v", clean.Paths)
	}
	if clean.TranscriptPath != "" || clean.Raw != nil {
		t.Fatal("unsafe payload retained")
	}
	if strings.Contains(clean.Prompt, "ghp_abcdefghijklmnopqrstuvwxyz") {
		t.Fatal("prompt token retained")
	}
}

func TestSanitizedMetadataKeepsStableSessionIdentity(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	e := Event{Kind: TurnStart, Agent: "agent-" + secret, AgentSessionID: secret, Worktree: "/repo/" + secret, ContextType: secret, GitSHA: secret, Paths: []string{"src/" + secret, "src/main.go"}, TranscriptPath: "/repo/" + secret}
	first := e.Sanitized()
	second := e.Sanitized()
	if first.AgentSessionID == secret || first.AgentSessionID != second.AgentSessionID || first.AgentSessionID != first.Sanitized().AgentSessionID {
		t.Fatalf("session identity is not safely stable: %q", first.AgentSessionID)
	}
	if strings.Contains(first.Agent, secret) || strings.Contains(first.Worktree, secret) || strings.Contains(first.ContextType, secret) || strings.Contains(first.GitSHA, secret) || first.TranscriptPath != "" || len(first.Paths) != 1 || first.Paths[0] != "src/main.go" {
		t.Fatalf("sensitive metadata retained: %+v", first)
	}
}
