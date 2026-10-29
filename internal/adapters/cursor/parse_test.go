package cursor

import (
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          event.Kind
	}{
		{"sessionStart", `{"conversation_id":"s","hook_event_name":"sessionStart"}`, event.SessionStart},
		{"beforeSubmitPrompt", `{"conversation_id":"s","hook_event_name":"beforeSubmitPrompt","prompt":"hello"}`, event.TurnStart},
		{"afterFileEdit", `{"conversation_id":"s","hook_event_name":"afterFileEdit","file_path":"/repo/a.go"}`, event.ToolEdit},
		{"beforeReadFile", `{"conversation_id":"s","hook_event_name":"beforeReadFile","file_path":"/repo/a.go"}`, event.FileRead},
		{"afterAgentThought", `{"conversation_id":"s","hook_event_name":"afterAgentThought","text":"thinking"}`, event.TurnContext},
		{"afterAgentResponse", `{"conversation_id":"s","hook_event_name":"afterAgentResponse","text":"done"}`, event.TurnContext},
		{"subagentStop", `{"conversation_id":"s","hook_event_name":"subagentStop","modified_files":["a.go"]}`, event.ToolEdit},
	} {
		got, err := Parse(tc.name, []byte(tc.payload), "/repo")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Kind != tc.want {
			t.Fatalf("%s: %+v", tc.name, got)
		}
	}
}

func TestSubagentSummaryIsRedacted(t *testing.T) {
	got, err := Parse("subagentStop", []byte(`{"conversation_id":"s","summary":"found ghp_abcdefghijklmnopqrstuvwxyz"}`), "/repo")
	if err != nil || len(got) != 2 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnContext || got[1].ContextType != "output" || strings.Contains(got[1].Text, "ghp_abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("events: %+v, %v", got, err)
	}
}

func TestTurnAndSessionEnd(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		kinds         []event.Kind
	}{
		{"stop", `{"conversation_id":"s"}`, []event.Kind{event.ToolEdit, event.TurnEnd}},
		{"sessionEnd", `{"session_id":"s"}`, []event.Kind{event.ToolEdit, event.TurnEnd, event.SessionEnd}},
		{"stop", `{}`, nil},
	} {
		t.Run(tc.name+tc.payload, func(t *testing.T) {
			got, err := Parse(tc.name, []byte(tc.payload), "/repo")
			if err != nil || len(got) != len(tc.kinds) {
				t.Fatalf("events: %+v, %v", got, err)
			}
			for i, kind := range tc.kinds {
				if got[i].Kind != kind || got[i].AgentSessionID != "s" {
					t.Fatalf("events: %+v", got)
				}
			}
		})
	}
}
