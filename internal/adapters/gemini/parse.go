package gemini

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

type payload struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	TranscriptPath string `json:"transcript_path"`
	Prompt         string `json:"prompt"`
	PromptResponse string `json:"prompt_response"`
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
	if p.SessionID == "" {
		return nil, nil
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
			if !filepath.IsAbs(path) && filepath.IsAbs(p.CWD) {
				path = filepath.Join(p.CWD, path)
			}
			ev.Paths = []string{path}
		}
	case "AfterAgent":
		scan := ev
		scan.Kind = event.ToolEdit
		if p.PromptResponse != "" {
			ev.Kind, ev.ContextType = event.TurnContext, "response"
			ev.Text = textutil.TruncateBytes(p.PromptResponse, 64<<10)
			end := ev
			end.Kind, end.Text, end.ContextType = event.TurnEnd, "", ""
			return []event.Event{scan.Sanitized(), ev.Sanitized(), end.Sanitized()}, nil
		}
		ev.Kind = event.TurnEnd
		return []event.Event{scan.Sanitized(), ev.Sanitized()}, nil
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
