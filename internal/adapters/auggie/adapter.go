package auggie

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

// Parse maps Auggie's documented hook payloads to Trace events. Tool output
// and file contents are intentionally ignored.
func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p struct {
		ConversationID string   `json:"conversation_id"`
		HookEventName  string   `json:"hook_event_name"`
		CWD            string   `json:"cwd"`
		WorkspaceRoots []string `json:"workspace_roots"`
		Prompt         string   `json:"user_prompt"`
		Tool           string   `json:"tool_name"`
		ToolError      bool     `json:"tool_is_error"`
		Conversation   struct {
			Response string `json:"agentTextResponse"`
		} `json:"conversation"`
		Input struct {
			Path string `json:"path"`
		} `json:"tool_input"`
		FileChanges []struct {
			Path string `json:"path"`
		} `json:"file_changes"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Auggie hook: %w", err)
	}
	if p.ConversationID == "" || (p.HookEventName != "" && p.HookEventName != name) {
		return nil, nil
	}
	e := event.Event{Agent: "auggie", AgentSessionID: p.ConversationID, Worktree: root, At: time.Now().UnixMilli()}
	switch name {
	case "SessionStart":
		e.Kind = event.SessionStart
	case "PromptSubmit":
		e.Kind, e.Prompt = event.TurnStart, p.Prompt
	case "Stop":
		scan := e
		scan.Kind = event.ToolEdit
		e.Kind = event.TurnEnd
		if p.Conversation.Response != "" {
			context := e
			context.Kind, context.ContextType = event.TurnContext, "response"
			context.Text = textutil.TruncateBytes(p.Conversation.Response, 64<<10)
			return []event.Event{scan.Sanitized(), context.Sanitized(), e.Sanitized()}, nil
		}
		return []event.Event{scan.Sanitized(), e.Sanitized()}, nil
	case "SessionEnd":
		scan := e
		scan.Kind = event.ToolEdit
		end := e
		end.Kind = event.TurnEnd
		e.Kind = event.SessionEnd
		return []event.Event{scan.Sanitized(), end.Sanitized(), e.Sanitized()}, nil
	case "PostToolUse":
		if p.ToolError && len(p.FileChanges) == 0 {
			return nil, nil
		}
		switch {
		case len(p.FileChanges) > 0:
			e.Kind = event.ToolEdit
		case p.Tool == "edit" || p.Tool == "write" || p.Tool == "bash" || p.Tool == "str-replace-editor" || p.Tool == "save-file" || p.Tool == "launch-process":
			e.Kind = event.ToolEdit
		case p.Tool == "read" || p.Tool == "view":
			e.Kind = event.FileRead
		default:
			return nil, nil
		}
		e.ToolName = p.Tool
		base := root
		ambiguous := false
		if len(p.WorkspaceRoots) > 0 {
			base = filepath.Clean(p.WorkspaceRoots[0])
			if !filepath.IsAbs(base) {
				ambiguous = true
			}
			for _, workspace := range p.WorkspaceRoots[1:] {
				if filepath.Clean(workspace) != base {
					ambiguous = true
				}
			}
		} else if filepath.IsAbs(p.CWD) {
			base = p.CWD
		}
		paths := []string{p.Input.Path}
		for _, change := range p.FileChanges {
			paths = append(paths, change.Path)
		}
		seen := make(map[string]bool)
		for _, path := range paths {
			if path == "" {
				continue
			}
			if !filepath.IsAbs(path) {
				if ambiguous {
					if e.Kind == event.ToolEdit {
						e.Paths = nil
						return []event.Event{e.Sanitized()}, nil
					}
					continue
				}
				path = filepath.Join(base, path)
			}
			path = filepath.Clean(path)
			if !seen[path] {
				e.Paths = append(e.Paths, path)
				seen[path] = true
			}
		}
	default:
		return nil, nil
	}
	return []event.Event{e.Sanitized()}, nil
}
