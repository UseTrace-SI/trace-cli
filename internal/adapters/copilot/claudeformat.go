package copilot

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// Copilot CLI also runs the hooks in a repository's .claude/settings.json
// and .claude/settings.local.json, so a repository with Trace's Claude Code
// hooks invokes `trace hook claude-code <Event>` from Copilot sessions.
// GitHub documents two payload shapes for those calls: camelCase fields
// (sessionId, toolName, toolArgs, numeric timestamp) and, for PascalCase
// event names, snake_case fields matching the VS Code format, which always
// carry a "timestamp" (ISO 8601). Claude Code's own hook payloads have
// neither "sessionId" nor "timestamp", so either key identifies Copilot.

// claudeEvents maps the event names Trace installs for Claude Code, and
// Copilot's own names, to the Copilot events Parse understands.
var claudeEvents = map[string]string{
	"SessionStart":        "sessionStart",
	"UserPromptSubmit":    "userPromptSubmitted",
	"PostToolUse":         "postToolUse",
	"Stop":                "agentStop",
	"SessionEnd":          "sessionEnd",
	"sessionStart":        "sessionStart",
	"userPromptSubmitted": "userPromptSubmitted",
	"postToolUse":         "postToolUse",
	"agentStop":           "agentStop",
	"sessionEnd":          "sessionEnd",
}

// claudeTools maps Claude tool names, used in the snake_case shape, to
// Copilot's.
var claudeTools = map[string]string{
	"Write": "create", "Edit": "edit", "MultiEdit": "edit", "Bash": "bash", "Read": "view",
}

// IsClaudeHookPayload reports whether a payload delivered to a Claude Code
// hook was sent by Copilot CLI.
func IsClaudeHookPayload(body []byte) bool {
	var keys map[string]json.RawMessage
	if json.Unmarshal(body, &keys) != nil {
		return false
	}
	_, camel := keys["sessionId"]
	_, stamped := keys["timestamp"]
	return camel || stamped
}

// ClaudeHookEvent is the Copilot event a Claude-format hook name stands
// for; ok is false for events Trace does not record from Copilot.
func ClaudeHookEvent(name string) (string, bool) {
	ev, ok := claudeEvents[name]
	return ev, ok
}

// ParseClaudeHook decodes a Copilot payload delivered to a Claude Code hook.
func ParseClaudeHook(name string, body []byte, root string) ([]event.Event, error) {
	var raw struct {
		SessionID  string          `json:"sessionId"`
		SessionID2 string          `json:"session_id"`
		AgentID    string          `json:"agentId"`
		AgentID2   string          `json:"agent_id"`
		CWD        string          `json:"cwd"`
		Timestamp  json.RawMessage `json:"timestamp"`
		Prompt     string          `json:"prompt"`
		ToolName   string          `json:"toolName"`
		ToolName2  string          `json:"tool_name"`
		ToolArgs   json.RawMessage `json:"toolArgs"`
		ToolInput  json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parsing Copilot hook: %w", err)
	}
	copilotEvent, ok := ClaudeHookEvent(name)
	if !ok {
		return nil, nil
	}
	p := payload{SessionID: first(raw.SessionID, raw.SessionID2), AgentID: first(raw.AgentID, raw.AgentID2), CWD: raw.CWD, Prompt: raw.Prompt, ToolArgs: raw.ToolArgs}
	if len(p.ToolArgs) == 0 {
		p.ToolArgs = raw.ToolInput
	}
	p.ToolName = first(raw.ToolName, raw.ToolName2)
	if mapped, ok := claudeTools[p.ToolName]; ok {
		p.ToolName = mapped
	} else {
		p.ToolName = strings.ToLower(p.ToolName)
	}
	p.Timestamp = timestamp(raw.Timestamp)
	return parse(copilotEvent, p, root)
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// timestamp accepts Unix milliseconds or an RFC 3339 string; 0 means now.
func timestamp(raw json.RawMessage) int64 {
	var ms int64
	if json.Unmarshal(raw, &ms) == nil {
		return ms
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if at, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return at.UnixMilli()
		}
	}
	return 0
}
