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

func TestAfterAgentCapturesResponse(t *testing.T) {
	events, err := Parse("AfterAgent", []byte(`{"session_id":"s","hook_event_name":"AfterAgent","prompt_response":"Done."}`), "/repo")
	if err != nil || len(events) != 3 || events[0].Kind != event.ToolEdit || events[1].Kind != event.TurnContext || events[1].Text != "Done." || events[2].Kind != event.TurnEnd {
		t.Fatalf("AfterAgent = %+v, %v", events, err)
	}
}

func TestEndAndRelativePaths(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []event.Kind
	}{
		{"AfterAgent", `{"session_id":"s"}`, []event.Kind{event.ToolEdit, event.TurnEnd}},
		{"SessionEnd", `{"session_id":"s"}`, []event.Kind{event.ToolEdit, event.TurnEnd, event.SessionEnd}},
		{"BeforeAgent", `{}`, nil},
		{"AfterTool", `{"session_id":"s","cwd":"/repo/sub","tool_name":"write_file","tool_input":{"file_path":"a.go"}}`, []event.Kind{event.ToolEdit}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.name, []byte(tc.body), "/repo")
			if err != nil || len(got) != len(tc.want) {
				t.Fatalf("events: %+v, %v", got, err)
			}
			for i, kind := range tc.want {
				if got[i].Kind != kind {
					t.Fatalf("events: %+v", got)
				}
			}
			if tc.name == "AfterTool" && (len(got[0].Paths) != 1 || got[0].Paths[0] != "/repo/sub/a.go") {
				t.Fatalf("relative path: %+v", got)
			}
		})
	}
}
