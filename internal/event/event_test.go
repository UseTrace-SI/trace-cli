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
