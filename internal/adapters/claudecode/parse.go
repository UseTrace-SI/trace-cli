package claudecode

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type payload struct {
	SessionID            string `json:"session_id"`
	AgentID              string `json:"agent_id"`
	TranscriptPath       string `json:"transcript_path"`
	CWD                  string `json:"cwd"`
	HookEventName        string `json:"hook_event_name"`
	Prompt               string `json:"prompt"`
	ToolName             string `json:"tool_name"`
	LastAssistantMessage string `json:"last_assistant_message"`
	ToolInput            struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Claude hook: %w", err)
	}
	if p.HookEventName != "" && p.HookEventName != name {
		return nil, fmt.Errorf("claude event name mismatch: %s != %s", p.HookEventName, name)
	}
	if p.SessionID == "" || (p.AgentID != "" && name != "PostToolUse") {
		return nil, nil
	}
	ev := event.Event{Agent: "claude-code", AgentSessionID: p.SessionID, Worktree: root, At: time.Now().UnixMilli(), TranscriptPath: p.TranscriptPath}
	switch name {
	case "SessionStart":
		ev.Kind = event.SessionStart
	case "UserPromptSubmit":
		ev.Kind = event.TurnStart
		ev.Prompt = p.Prompt
	case "PostToolUse":
		switch p.ToolName {
		case "Edit", "Write", "MultiEdit", "NotebookEdit", "Bash":
		default:
			return nil, nil
		}
		ev.Kind = event.ToolEdit
		ev.ToolName = p.ToolName
		if p.ToolInput.FilePath != "" {
			ev.Paths = []string{p.ToolInput.FilePath}
		}
		if p.ToolInput.NotebookPath != "" {
			ev.Paths = []string{p.ToolInput.NotebookPath}
		}
		if filepath.IsAbs(p.CWD) {
			for i, path := range ev.Paths {
				if !filepath.IsAbs(path) {
					ev.Paths[i] = filepath.Join(p.CWD, path)
				}
			}
		}
	case "Stop", "StopFailure":
		ev.Kind = event.TurnEnd
		ev.Text = p.LastAssistantMessage
	case "SessionEnd":
		ev.Kind = event.SessionEnd
	default:
		return nil, nil
	}
	return []event.Event{ev.Sanitized()}, nil
}

// ParseEvents closes unfinished turns on failure or session shutdown and
// requests a final scan for changes not reported by a tool hook.
func ParseEvents(name string, body []byte, root string) ([]event.Event, error) {
	events, err := Parse(name, body, root)
	if err != nil {
		return nil, err
	}
	var result []event.Event
	for _, ev := range events {
		if ev.Kind == event.TurnEnd || ev.Kind == event.SessionEnd {
			scan := ev
			scan.Kind, scan.Text, scan.Paths = event.ToolEdit, "", nil
			result = append(result, scan)
			if ev.Kind == event.SessionEnd {
				end := ev
				end.Kind = event.TurnEnd
				result = append(result, end)
			}
		}
		result = append(result, ev)
	}
	return result, nil
}
