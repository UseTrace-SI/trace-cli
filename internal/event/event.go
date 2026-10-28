package event

import (
	"encoding/json"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
)

type Kind string

const (
	SessionStart Kind = "session_start"
	TurnStart    Kind = "turn_start"
	ToolEdit     Kind = "tool_edit"
	FileRead     Kind = "file_read"
	TurnContext  Kind = "turn_context"
	TurnEnd      Kind = "turn_end"
	SessionEnd   Kind = "session_end"
	GitCommit    Kind = "git_commit"
)

type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}
type Event struct {
	Kind           Kind            `json:"kind"`
	Agent          string          `json:"agent"`
	AgentSessionID string          `json:"agent_session_id,omitempty"`
	Worktree       string          `json:"worktree"`
	At             int64           `json:"at"`
	Prompt         string          `json:"prompt,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	ContextType    string          `json:"context_type,omitempty"`
	Text           string          `json:"text,omitempty"`
	Paths          []string        `json:"paths,omitempty"`
	Lines          *LineRange      `json:"lines,omitempty"`
	TranscriptPath string          `json:"transcript_path,omitempty"`
	GitSHA         string          `json:"git_sha,omitempty"`
	Raw            json.RawMessage `json:"raw,omitempty"`
}

func (e Event) Sanitized() Event {
	r := redact.Redactor{}
	e.Prompt = r.Text(e.Prompt)
	e.ToolName = r.Text(e.ToolName)
	e.Text = r.Text(e.Text)
	e.Raw = nil
	paths := make([]string, 0, len(e.Paths))
	for _, path := range e.Paths {
		if !manifest.SecretPath(path) {
			paths = append(paths, path)
		}
	}
	e.Paths = paths
	if manifest.SecretPath(e.TranscriptPath) {
		e.TranscriptPath = ""
	}
	if e.At == 0 {
		e.At = time.Now().UnixMilli()
	}
	return e
}
