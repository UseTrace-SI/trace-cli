package codexhook

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/codex"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

const configPath = ".codex/hooks.json"

var events = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "Interrupt", "SessionEnd"}

func Install(home string) error {
	root, err := codex.ConfigHome(home)
	if err != nil {
		return err
	}
	return hookconfig.Install(root, "codex", "hooks.json", events, true)
}
func Uninstall(home string) error {
	root, err := codex.ConfigHome(home)
	if err != nil {
		return err
	}
	return hookconfig.Uninstall(root, "codex", "hooks.json", true)
}

type payload struct {
	SessionID            string `json:"session_id"`
	Prompt               string `json:"prompt"`
	LastAssistantMessage string `json:"last_assistant_message"`
	TranscriptPath       string `json:"transcript_path"`
	ToolName             string `json:"tool_name"`
	ToolInput            struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	} `json:"tool_input"`
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Codex hook: %w", err)
	}
	ev := event.Event{Agent: "codex", AgentSessionID: p.SessionID, Worktree: root, At: time.Now().UnixMilli(), TranscriptPath: p.TranscriptPath}
	switch name {
	case "SessionStart":
		ev.Kind = event.SessionStart
	case "UserPromptSubmit":
		ev.Kind, ev.Prompt = event.TurnStart, p.Prompt
	case "PostToolUse":
		switch p.ToolName {
		case "Write", "Edit", "apply_patch", "Bash", "exec_command":
			ev.Kind = event.ToolEdit
		case "Read":
			ev.Kind = event.FileRead
		default:
			return nil, nil
		}
		ev.ToolName = p.ToolName
		path := p.ToolInput.FilePath
		if path == "" {
			path = p.ToolInput.Path
		}
		if path != "" {
			ev.Paths = []string{path}
		}
	case "Stop":
		// A failed or unobserved tool can have written files before the hook.
		// Request a fallback scan at the turn boundary regardless of tool hooks.
		scan := ev
		scan.Kind = event.ToolEdit
		if p.LastAssistantMessage != "" {
			ev.Kind, ev.ContextType, ev.Text = event.TurnContext, "response", textutil.TruncateBytes(p.LastAssistantMessage, 64<<10)
			end := ev
			end.Kind, end.ContextType, end.Text = event.TurnEnd, "", ""
			return []event.Event{scan.Sanitized(), ev.Sanitized(), end.Sanitized()}, nil
		}
		ev.Kind = event.TurnEnd
		return []event.Event{scan.Sanitized(), ev.Sanitized()}, nil
	case "Interrupt":
		// A cancelled tool may have written files before its PostToolUse hook.
		// Request a fallback scan before closing the interrupted turn.
		ev.Kind = event.ToolEdit
		end := ev
		end.Kind = event.TurnEnd
		return []event.Event{ev.Sanitized(), end.Sanitized()}, nil
	case "SessionEnd":
		ev.Kind = event.SessionEnd
	default:
		return nil, nil
	}
	return []event.Event{ev.Sanitized()}, nil
}
