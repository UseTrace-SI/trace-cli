package copilot

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type payload struct {
	SessionID string          `json:"sessionId"`
	AgentID   string          `json:"agentId"`
	CWD       string          `json:"cwd"`
	Timestamp int64           `json:"timestamp"`
	Prompt    string          `json:"prompt"`
	ToolName  string          `json:"toolName"`
	ToolArgs  json.RawMessage `json:"toolArgs"`
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Copilot hook: %w", err)
	}
	if p.SessionID == "" || (p.AgentID != "" && name != "postToolUse") {
		return nil, nil
	}
	at := p.Timestamp
	if at == 0 {
		at = time.Now().UnixMilli()
	}
	ev := event.Event{Agent: "copilot", AgentSessionID: p.SessionID, Worktree: root, At: at}
	switch name {
	case "sessionStart":
		ev.Kind = event.SessionStart
	case "userPromptSubmitted":
		ev.Kind, ev.Prompt = event.TurnStart, p.Prompt
	case "postToolUse":
		switch p.ToolName {
		case "create", "edit", "str_replace_editor", "apply_patch", "bash", "powershell":
			ev.Kind, ev.ToolName = event.ToolEdit, p.ToolName
		case "view":
			ev.Kind, ev.ToolName = event.FileRead, p.ToolName
		default:
			return nil, nil
		}
		var args map[string]any
		if json.Unmarshal(p.ToolArgs, &args) != nil {
			var encoded string
			if json.Unmarshal(p.ToolArgs, &encoded) == nil {
				_ = json.Unmarshal([]byte(encoded), &args)
			}
		}
		for _, key := range []string{"path", "file_path", "filePath"} {
			if path, ok := args[key].(string); ok && path != "" {
				if !filepath.IsAbs(path) && filepath.IsAbs(p.CWD) {
					path = filepath.Join(p.CWD, path)
				}
				ev.Paths = []string{path}
				break
			}
		}
	case "agentStop":
		scan := ev
		scan.Kind = event.ToolEdit
		ev.Kind = event.TurnEnd
		return []event.Event{scan.Sanitized(), ev.Sanitized()}, nil
	case "sessionEnd":
		// Aborts and timeouts can end a session without an agentStop event.
		// Close any open turn before ending the session so its edits are saved.
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
