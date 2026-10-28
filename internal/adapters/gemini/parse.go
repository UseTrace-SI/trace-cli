package gemini

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type payload struct {
	SessionID      string `json:"session_id"`
	HookEventName  string `json:"hook_event_name"`
	TranscriptPath string `json:"transcript_path"`
	Prompt         string `json:"prompt"`
	ToolName       string `json:"tool_name"`
	ToolInput      struct {
		FilePath     string `json:"file_path"`
		AbsolutePath string `json:"absolute_path"`
	} `json:"tool_input"`
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Gemini hook: %w", err)
	}
	if p.HookEventName != "" && p.HookEventName != name {
		return nil, fmt.Errorf("gemini event name mismatch")
	}
	ev := event.Event{Agent: "gemini", AgentSessionID: p.SessionID, Worktree: root, At: time.Now().UnixMilli(), TranscriptPath: p.TranscriptPath}
	switch name {
	case "SessionStart":
		ev.Kind = event.SessionStart
	case "BeforeAgent":
		ev.Kind = event.TurnStart
		ev.Prompt = p.Prompt
	case "AfterTool":
		switch p.ToolName {
		case "write_file", "replace", "run_shell_command":
		default:
			return nil, nil
		}
		ev.Kind = event.ToolEdit
		ev.ToolName = p.ToolName
		path := p.ToolInput.FilePath
		if path == "" {
			path = p.ToolInput.AbsolutePath
		}
		if path != "" {
			ev.Paths = []string{path}
		}
	case "AfterAgent":
		ev.Kind = event.TurnEnd
	case "SessionEnd":
		ev.Kind = event.SessionEnd
	default:
		return nil, nil
	}
	return []event.Event{ev.Sanitized()}, nil
}
