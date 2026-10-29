package auggie

import (
	"slices"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name, body string
		kind       event.Kind
		count      int
	}{
		{"SessionStart", `{"conversation_id":"one"}`, event.SessionStart, 1},
		{"PromptSubmit", `{"conversation_id":"one","user_prompt":"fix token sk-abcdefghijklmnopqrstuvwxyz1234567890"}`, event.TurnStart, 1},
		{"PostToolUse", `{"conversation_id":"one","tool_name":"edit","tool_input":{"path":"a.go"}}`, event.ToolEdit, 1},
		{"PostToolUse", `{"conversation_id":"one","tool_name":"read"}`, event.FileRead, 1},
		{"PostToolUse", `{"conversation_id":"one","tool_name":"edit","tool_is_error":true}`, "", 0},
		{"Stop", `{"conversation_id":"one","agent_stop_cause":"end_turn"}`, event.ToolEdit, 2},
		{"SessionEnd", `{"conversation_id":"one"}`, event.ToolEdit, 3},
		{"Stop", `{"conversation_id":"one","hook_event_name":"SessionEnd"}`, "", 0},
		{"PromptSubmit", `{"user_prompt":"missing session"}`, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name+tt.body, func(t *testing.T) {
			got, err := Parse(tt.name, []byte(tt.body), "/repo")
			if err != nil || len(got) != tt.count {
				t.Fatalf("got %#v, %v", got, err)
			}
			if len(got) > 0 && (got[0].Kind != tt.kind || got[0].AgentSessionID != "one" || strings.Contains(got[0].Prompt, "sk-abcdefghijklmnopqrstuvwxyz")) {
				t.Fatalf("event: %#v", got[0])
			}
			if tt.name == "Stop" && len(got) == 2 && got[1].Kind != event.TurnEnd {
				t.Fatalf("turn not closed: %#v", got)
			}
			if tt.name == "SessionEnd" && len(got) == 3 && (got[1].Kind != event.TurnEnd || got[2].Kind != event.SessionEnd) {
				t.Fatalf("session not closed: %#v", got)
			}
		})
	}
}

func TestStopResponseAndRelativeToolPath(t *testing.T) {
	got, err := Parse("Stop", []byte(`{"conversation_id":"one","conversation":{"agentTextResponse":"done ghp_abcdefghijklmnopqrstuvwxyz","agentCodeResponse":[{"content":"ignore me"}]}}`), "/repo")
	if err != nil || len(got) != 3 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnContext || got[2].Kind != event.TurnEnd || strings.Contains(got[1].Text, "ghp_abcdefghijklmnopqrstuvwxyz") || strings.Contains(got[1].Text, "ignore me") {
		t.Fatalf("stop: %+v, %v", got, err)
	}
	got, err = Parse("PostToolUse", []byte(`{"conversation_id":"one","tool_name":"edit","tool_input":{"path":"src/a.go"}}`), "/repo")
	if err != nil || len(got) != 1 || len(got[0].Paths) != 1 || got[0].Paths[0] != "/repo/src/a.go" {
		t.Fatalf("path: %+v, %v", got, err)
	}
}

func TestFileChanges(t *testing.T) {
	got, err := Parse("PostToolUse", []byte(`{"conversation_id":"one","tool_name":"other","tool_is_error":true,"tool_input":{"path":"src/a.go"},"file_changes":[{"path":"src/a.go","content":"never store this"},{"path":"/repo/src/b.go","oldContent":"never store this"}]}`), "/repo")
	if err != nil || len(got) != 1 || got[0].Kind != event.ToolEdit || len(got[0].Paths) != 2 || got[0].Paths[0] != "/repo/src/a.go" || got[0].Paths[1] != "/repo/src/b.go" || strings.Contains(got[0].Text, "never store this") {
		t.Fatalf("changes: %+v, %v", got, err)
	}
}

func TestWorkspaceRelativePaths(t *testing.T) {
	tests := []struct {
		name, body string
		paths      []string
	}{
		{"nested workspace", `{"conversation_id":"one","workspace_roots":["/repo/sub"],"tool_name":"edit","file_changes":[{"path":"a.go"}]}`, []string{"/repo/sub/a.go"}},
		{"session cwd", `{"conversation_id":"one","cwd":"/repo/sub","tool_name":"read","tool_input":{"path":"a.go"}}`, []string{"/repo/sub/a.go"}},
		{"ambiguous workspaces scan", `{"conversation_id":"one","workspace_roots":["/repo/a","/repo/b"],"tool_name":"edit","file_changes":[{"path":"/repo/a/first.go"},{"path":"second.go"}]}`, nil},
		{"ambiguous read", `{"conversation_id":"one","workspace_roots":["/repo/a","/repo/b"],"tool_name":"read","tool_input":{"path":"a.go"}}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse("PostToolUse", []byte(tt.body), "/repo")
			if err != nil || len(got) != 1 || !slices.Equal(got[0].Paths, tt.paths) {
				t.Fatalf("got %+v, %v; want paths %v", got, err, tt.paths)
			}
		})
	}
}
