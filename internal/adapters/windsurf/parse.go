package windsurf

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

type payload struct {
	Action       string `json:"agent_action_name"`
	TrajectoryID string `json:"trajectory_id"`
	Timestamp    string `json:"timestamp"`
	ToolInfo     struct {
		UserPrompt string `json:"user_prompt"`
		FilePath   string `json:"file_path"`
		Response   string `json:"response"`
	} `json:"tool_info"`
}

// Parse maps documented Cascade hooks to Trace events. It does not read the
// full transcript: that file may include raw code and command output.
func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Windsurf hook: %w", err)
	}
	if p.Action != "" && p.Action != name {
		return nil, fmt.Errorf("windsurf event name mismatch")
	}
	at := time.Now().UnixMilli()
	if p.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, p.Timestamp); err == nil {
			at = t.UnixMilli()
		}
	}
	ev := event.Event{Agent: "windsurf", AgentSessionID: p.TrajectoryID, Worktree: root, At: at}
	switch name {
	case "pre_user_prompt":
		ev.Kind, ev.Prompt = event.TurnStart, p.ToolInfo.UserPrompt
	case "post_read_code":
		ev.Kind = event.FileRead
		if p.ToolInfo.FilePath != "" {
			ev.Paths = []string{p.ToolInfo.FilePath}
		}
	case "post_write_code":
		ev.Kind, ev.ToolName = event.ToolEdit, "Write"
		if p.ToolInfo.FilePath != "" {
			ev.Paths = []string{p.ToolInfo.FilePath}
		}
	case "post_run_command":
		ev.Kind, ev.ToolName = event.ToolEdit, "Shell"
	case "post_cascade_response":
		ev.Kind, ev.ContextType = event.TurnContext, "response"
		ev.Text = textutil.TruncateBytes(p.ToolInfo.Response, 64<<10)
		end := ev
		end.Kind, end.Text, end.ContextType = event.TurnEnd, "", ""
		return []event.Event{ev.Sanitized(), end.Sanitized()}, nil
	default:
		return nil, nil
	}
	return []event.Event{ev.Sanitized()}, nil
}
