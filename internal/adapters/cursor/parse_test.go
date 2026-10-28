package cursor

import (
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
		{"stop", `{"conversation_id":"s","hook_event_name":"stop"}`, event.TurnEnd},
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
