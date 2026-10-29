package factory

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

type payload struct {
	SessionID        string          `json:"session_id"`
	HookEventName    string          `json:"hook_event_name"`
	Prompt           string          `json:"prompt"`
	ToolName         string          `json:"tool_name"`
	CWD              string          `json:"cwd"`
	NotificationType string          `json:"notification_type"`
	TaskResult       json.RawMessage `json:"task_result"`
	ToolInput        struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	} `json:"tool_input"`
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Factory hook: %w", err)
	}
	if p.HookEventName != "" && p.HookEventName != name {
		return nil, fmt.Errorf("factory event name mismatch")
	}
	if p.SessionID == "" {
		return nil, nil
	}
	ev := event.Event{Agent: "factory", AgentSessionID: p.SessionID, Worktree: root, At: time.Now().UnixMilli()}
	switch name {
	case "SessionStart":
		ev.Kind = event.SessionStart
	case "UserPromptSubmit":
		ev.Kind, ev.Prompt = event.TurnStart, p.Prompt
	case "PostToolUse":
		switch p.ToolName {
		case "Create", "Edit", "ApplyPatch", "Execute":
			ev.Kind, ev.ToolName = event.ToolEdit, p.ToolName
		case "Read":
			ev.Kind, ev.ToolName = event.FileRead, p.ToolName
		default:
			return nil, nil
		}
		path := p.ToolInput.FilePath
		if path == "" {
			path = p.ToolInput.Path
		}
		if path != "" {
			if !filepath.IsAbs(path) && filepath.IsAbs(p.CWD) {
				path = filepath.Join(p.CWD, path)
			}
			ev.Paths = []string{path}
		}
	case "Notification":
		if p.NotificationType != "idle_prompt" {
			return nil, nil
		}
		fallthrough
	case "Stop":
		// Shell commands and patch tools may not report individual paths.
		scan := ev
		scan.Kind = event.ToolEdit
		ev.Kind = event.TurnEnd
		return []event.Event{scan.Sanitized(), ev.Sanitized()}, nil
	case "SubagentStop":
		ev.Kind, ev.ToolName = event.ToolEdit, "Subagent"
		var result string
		if json.Unmarshal(p.TaskResult, &result) == nil && result != "" {
			context := ev
			context.Kind, context.ContextType, context.ToolName = event.TurnContext, "output", ""
			context.Text = textutil.TruncateBytes(result, 64<<10)
			return []event.Event{ev.Sanitized(), context.Sanitized()}, nil
		}
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
