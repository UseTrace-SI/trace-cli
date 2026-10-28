package agy

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type hookPayload struct {
	ConversationID string `json:"conversationId"`
	TranscriptPath string `json:"transcriptPath"`
	FullyIdle      bool   `json:"fullyIdle"`
	ToolCall       struct {
		Name string `json:"name"`
		Args struct {
			TargetFile   string `json:"TargetFile"`
			AbsolutePath string `json:"AbsolutePath"`
		} `json:"args"`
	} `json:"toolCall"`
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p hookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Antigravity hook: %w", err)
	}
	if p.ConversationID == "" {
		return nil, fmt.Errorf("antigravity hook has no conversationId")
	}
	ev := event.Event{Agent: "agy", AgentSessionID: p.ConversationID, Worktree: root,
		At: time.Now().UnixMilli(), TranscriptPath: p.TranscriptPath}
	switch name {
	case "SessionStart":
		ev.Kind = event.SessionStart
	case "PreInvocation":
		ev.Kind = event.TurnStart
	case "PostToolUse":
		switch p.ToolCall.Name {
		case "write_to_file", "replace_file_content", "multi_replace_file_content", "run_command":
		default:
			return nil, nil
		}
		ev.Kind = event.ToolEdit
		ev.ToolName = p.ToolCall.Name
		path := p.ToolCall.Args.TargetFile
		if path == "" {
			path = p.ToolCall.Args.AbsolutePath
		}
		if path != "" {
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			ev.Paths = []string{path}
		}
	case "Stop":
		if !p.FullyIdle {
			return nil, nil
		}
		ev.Kind = event.TurnEnd
	default:
		return nil, nil
	}
	return []event.Event{ev.Sanitized()}, nil
}
