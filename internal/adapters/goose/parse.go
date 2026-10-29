package goose

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

type payload struct {
	SessionID            string `json:"session_id"`
	Event                string `json:"event"`
	WorkingDir           string `json:"working_dir"`
	LastAssistantMessage string `json:"last_assistant_message"`
	Message              string `json:"message"`
	ToolName             string `json:"tool_name"`
	ToolInput            struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing goose hook: %w", err)
	}
	if p.SessionID == "" || (p.Event != "" && p.Event != name) {
		return nil, nil
	}
	ev := event.Event{Agent: "goose", AgentSessionID: p.SessionID, Worktree: root, At: time.Now().UnixMilli()}
	switch name {
	case "SessionStart":
		ev.Kind = event.SessionStart
	case "UserPromptSubmit":
		ev.Kind, ev.Prompt = event.TurnStart, p.Message
	case "PostToolUse":
		switch strings.TrimPrefix(p.ToolName, "developer__") {
		case "write", "edit", "text_editor", "shell", "bash":
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
			if !filepath.IsAbs(path) && filepath.IsAbs(p.WorkingDir) {
				path = filepath.Join(p.WorkingDir, path)
			}
			ev.Paths = []string{path}
		}
	case "Stop":
		scan := ev
		scan.Kind = event.ToolEdit
		result := []event.Event{scan.Sanitized()}
		if p.LastAssistantMessage != "" {
			context := ev
			context.Kind, context.ContextType = event.TurnContext, "response"
			context.Text = textutil.TruncateBytes(p.LastAssistantMessage, 64<<10)
			result = append(result, context.Sanitized())
		}
		ev.Kind = event.TurnEnd
		return append(result, ev.Sanitized()), nil
	case "SessionEnd":
		scan := ev
		scan.Kind = event.ToolEdit
		end := ev
		end.Kind = event.TurnEnd
		ev.Kind = event.SessionEnd
		return []event.Event{scan.Sanitized(), end.Sanitized(), ev.Sanitized()}, nil
	default:
		return nil, nil
	}
	return []event.Event{ev.Sanitized()}, nil
}
