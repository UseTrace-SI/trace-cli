package grok

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

type payload struct {
	SessionID    string `json:"sessionId"`
	SubagentType string `json:"subagentType"`
	Prompt       string `json:"prompt"`
	ToolName     string `json:"toolName"`
	ToolInput    struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
	} `json:"toolInput"`
	LastAssistantMessage string `json:"lastAssistantMessage"`
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Grok hook: %w", err)
	}
	if p.SubagentType != "" {
		return nil, nil
	}
	ev := event.Event{Agent: "grok", AgentSessionID: p.SessionID, Worktree: root, At: time.Now().UnixMilli()}
	switch name {
	case "SessionStart":
		ev.Kind = event.SessionStart
	case "UserPromptSubmit":
		ev.Kind, ev.Prompt = event.TurnStart, p.Prompt
	case "PostToolUse":
		switch p.ToolName {
		case "write", "edit", "apply_patch", "run_terminal_command", "execute":
			ev.Kind = event.ToolEdit
		case "read":
			ev.Kind = event.FileRead
		default:
			return nil, nil
		}
		ev.ToolName = p.ToolName
		path := p.ToolInput.Path
		if path == "" {
			path = p.ToolInput.FilePath
		}
		if path != "" {
			ev.Paths = []string{path}
		}
	case "Stop":
		if p.LastAssistantMessage != "" {
			ev.Kind, ev.ContextType, ev.Text = event.TurnContext, "response", textutil.TruncateBytes(p.LastAssistantMessage, 64<<10)
			end := ev
			end.Kind, end.ContextType, end.Text = event.TurnEnd, "", ""
			return []event.Event{ev.Sanitized(), end.Sanitized()}, nil
		}
		ev.Kind = event.TurnEnd
	case "SessionEnd":
		ev.Kind = event.SessionEnd
	default:
		return nil, nil
	}
	return []event.Event{ev.Sanitized()}, nil
}
