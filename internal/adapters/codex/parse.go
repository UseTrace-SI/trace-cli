package codex

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type Parser struct {
	Root, SessionID string
	Active          bool
}
type record struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

func (p *Parser) Feed(line []byte) ([]event.Event, error) {
	var r record
	if err := json.Unmarshal(line, &r); err != nil {
		return nil, fmt.Errorf("parsing Codex rollout: %w", err)
	}
	at := time.Now().UnixMilli()
	if t, err := time.Parse(time.RFC3339Nano, r.Timestamp); err == nil {
		at = t.UnixMilli()
	}
	var x struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
		Role      string `json:"role"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Summary []struct {
			Text string `json:"text"`
		} `json:"summary"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(r.Payload, &x); err != nil {
		return nil, fmt.Errorf("parsing Codex payload: %w", err)
	}
	if r.Type == "session_meta" {
		p.SessionID = x.SessionID
		if p.SessionID == "" {
			p.SessionID = x.ID
		}
		p.Active = filepath.Clean(x.CWD) == filepath.Clean(p.Root)
		if p.Active {
			return []event.Event{{Kind: event.SessionStart, Agent: "codex", AgentSessionID: p.SessionID, Worktree: p.Root, At: at}}, nil
		}
		return nil, nil
	}
	if !p.Active || p.SessionID == "" {
		return nil, nil
	}
	ev := event.Event{Agent: "codex", AgentSessionID: p.SessionID, Worktree: p.Root, At: at}
	switch {
	case r.Type == "response_item" && x.Type == "message" && x.Role == "user":
		ev.Kind = event.TurnStart
		var parts []string
		for _, c := range x.Content {
			if c.Type == "input_text" {
				parts = append(parts, c.Text)
			}
		}
		ev.Prompt = strings.Join(parts, "\n")
	case r.Type == "response_item" && (x.Type == "function_call" || x.Type == "custom_tool_call"):
		ev.Kind = event.ToolEdit
		ev.ToolName = x.Name
	case r.Type == "response_item" && x.Type == "message" && x.Role == "assistant":
		ev.Kind = event.TurnContext
		ev.ContextType = "response"
		var parts []string
		for _, c := range x.Content {
			if c.Type == "output_text" {
				parts = append(parts, c.Text)
			}
		}
		ev.Text = strings.Join(parts, "\n")
		if ev.Text == "" {
			return nil, nil
		}
	case r.Type == "response_item" && x.Type == "reasoning":
		ev.Kind = event.TurnContext
		ev.ContextType = "thinking"
		var parts []string
		for _, item := range x.Summary {
			parts = append(parts, item.Text)
		}
		ev.Text = strings.Join(parts, "\n")
		if ev.Text == "" {
			return nil, nil
		}
	case r.Type == "event_msg" && x.Type == "task_complete":
		ev.Kind = event.TurnEnd
	default:
		return nil, nil
	}
	return []event.Event{ev.Sanitized()}, nil
}
