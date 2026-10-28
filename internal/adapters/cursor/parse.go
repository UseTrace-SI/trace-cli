package cursor

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

type payload struct {
	ConversationID string `json:"conversation_id"`
	SessionID      string `json:"session_id"`
	HookEventName  string `json:"hook_event_name"`
	Prompt         string `json:"prompt"`
	Text           string `json:"text"`
	FilePath       string `json:"file_path"`
	TranscriptPath string `json:"transcript_path"`
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Cursor hook: %w", err)
	}
	if p.HookEventName != "" && p.HookEventName != name {
		return nil, fmt.Errorf("cursor event name mismatch")
	}
	sid := p.ConversationID
	if sid == "" {
		sid = p.SessionID
	}
	ev := event.Event{Agent: "cursor", AgentSessionID: sid, Worktree: root, At: time.Now().UnixMilli(), TranscriptPath: p.TranscriptPath}
	switch name {
	case "sessionStart":
		ev.Kind = event.SessionStart
	case "beforeSubmitPrompt":
		ev.Kind = event.TurnStart
		ev.Prompt = p.Prompt
	case "afterFileEdit":
		ev.Kind = event.ToolEdit
		ev.ToolName = "Write"
		if p.FilePath != "" {
			ev.Paths = []string{p.FilePath}
		}
	case "afterShellExecution":
		ev.Kind = event.ToolEdit
		ev.ToolName = "Shell"
	case "beforeReadFile":
		ev.Kind = event.FileRead
		if p.FilePath != "" {
			ev.Paths = []string{p.FilePath}
		}
	case "afterAgentThought", "afterAgentResponse":
		ev.Kind = event.TurnContext
		ev.Text = p.Text
		if len(ev.Text) > 64<<10 {
			ev.Text = textutil.TruncateBytes(ev.Text, 64<<10)
		}
		if name == "afterAgentThought" {
			ev.ContextType = "thinking"
		} else {
			ev.ContextType = "response"
		}
	case "stop":
		ev.Kind = event.TurnEnd
	case "sessionEnd":
		ev.Kind = event.SessionEnd
	default:
		return nil, nil
	}
	return []event.Event{ev.Sanitized()}, nil
}
