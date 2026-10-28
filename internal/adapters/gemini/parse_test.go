package gemini

import (
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          event.Kind
	}{
		{"SessionStart", `{"session_id":"s","hook_event_name":"SessionStart"}`, event.SessionStart},
		{"BeforeAgent", `{"session_id":"s","hook_event_name":"BeforeAgent","prompt":"hello"}`, event.TurnStart},
		{"AfterTool", `{"session_id":"s","hook_event_name":"AfterTool","tool_name":"write_file","tool_input":{"file_path":"/repo/a.go"}}`, event.ToolEdit},
		{"AfterAgent", `{"session_id":"s","hook_event_name":"AfterAgent"}`, event.TurnEnd},
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
