package qwen

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

type payload struct {
	SessionID            string `json:"session_id"`
	CWD                  string `json:"cwd"`
	AgentID              string `json:"agent_id"`
	HookEventName        string `json:"hook_event_name"`
	SubmittedPrompt      string `json:"submitted_prompt"`
	LastAssistantMessage string `json:"last_assistant_message"`
	ToolName             string `json:"tool_name"`
	ToolInput            struct {
		FilePath     string `json:"file_path"`
		AbsolutePath string `json:"absolute_path"`
		Path         string `json:"path"`
	} `json:"tool_input"`
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Qwen hook: %w", err)
	}
	if p.HookEventName != "" && p.HookEventName != name {
		return nil, fmt.Errorf("qwen event name mismatch")
	}
	// Qwen hooks can run inside subagents. Those events must not close or
	// replace the parent agent's active turn.
	if p.SessionID == "" || p.AgentID != "" {
		return nil, nil
	}
	ev := event.Event{Agent: "qwen", AgentSessionID: p.SessionID, Worktree: root, At: time.Now().UnixMilli()}
	switch name {
	case "SessionStart":
		ev.Kind = event.SessionStart
	case "UserPromptSubmit":
		if p.SubmittedPrompt == "" {
			return nil, nil
		}
		ev.Kind, ev.Prompt = event.TurnStart, p.SubmittedPrompt
	case "PostToolUse":
		switch p.ToolName {
		case "write_file", "replace", "edit", "run_shell_command":
			ev.Kind, ev.ToolName = event.ToolEdit, p.ToolName
		case "read_file":
			ev.Kind, ev.ToolName = event.FileRead, p.ToolName
		default:
			return nil, nil
		}
		path := p.ToolInput.FilePath
		if path == "" {
			path = p.ToolInput.AbsolutePath
		}
		if path == "" {
			path = p.ToolInput.Path
		}
		if path != "" {
			if !filepath.IsAbs(path) && filepath.IsAbs(p.CWD) {
				path = filepath.Join(p.CWD, path)
			}
			ev.Paths = []string{path}
		}
	case "Stop":
		scan := ev
		scan.Kind = event.ToolEdit
		if p.LastAssistantMessage != "" {
			ev.Kind, ev.ContextType = event.TurnContext, "response"
			ev.Text = textutil.TruncateBytes(p.LastAssistantMessage, 64<<10)
			end := ev
			end.Kind, end.Text, end.ContextType = event.TurnEnd, "", ""
			return []event.Event{scan.Sanitized(), ev.Sanitized(), end.Sanitized()}, nil
		}
		ev.Kind = event.TurnEnd
		return []event.Event{scan.Sanitized(), ev.Sanitized()}, nil
	case "StopFailure":
		// A failed turn may leave tool edits without a completed tool event.
		ev.Kind = event.ToolEdit
		end := ev
		end.Kind = event.TurnEnd
		return []event.Event{ev.Sanitized(), end.Sanitized()}, nil
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
