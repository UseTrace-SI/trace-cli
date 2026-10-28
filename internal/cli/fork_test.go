package cli

import (
	"reflect"
	"testing"
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
