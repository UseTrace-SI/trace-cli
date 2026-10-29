package openclaude

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/claudecode"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

var events = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "StopFailure", "SessionEnd"}

func Install(root string) error {
	return hookconfig.Install(root, "openclaude", ".openclaude/settings.local.json", events, true)
}
func Uninstall(root string) error {
	return hookconfig.Uninstall(root, "openclaude", ".openclaude/settings.local.json", true)
}
func InstallGlobal(configRoot string) error {
	return hookconfig.Install(configRoot, "openclaude", "settings.json", events, true)
}
func UninstallGlobal(configRoot string) error {
	return hookconfig.Uninstall(configRoot, "openclaude", "settings.json", true)
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var payload struct {
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
		AgentID   string `json:"agent_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parsing OpenClaude hook: %w", err)
	}
	if payload.SessionID == "" {
		return nil, nil
	}
	// Subagent tool edits belong to the parent turn, but subagent lifecycle
	// events (including StopFailure) must not end that turn.
	if payload.AgentID != "" && name != "PostToolUse" {
		return nil, nil
	}
	events, err := claudecode.Parse(name, body, root)
	if err != nil {
		return nil, err
	}
	var result []event.Event
	for _, ev := range events {
		ev.Agent = "openclaude"
		if filepath.IsAbs(payload.CWD) {
			for i, path := range ev.Paths {
				if !filepath.IsAbs(path) {
					ev.Paths[i] = filepath.Join(payload.CWD, path)
				}
			}
		}
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
		if ev.Kind == event.TurnEnd && ev.Text != "" {
			context := ev
			context.Kind, context.ContextType = event.TurnContext, "response"
			result = append(result, context)
			ev.Text = ""
		}
		result = append(result, ev)
	}
	return result, nil
}
