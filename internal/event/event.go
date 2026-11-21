package event

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
	GitRewrite   Kind = "git_rewrite"
)

// Rewrite is one "old new" pair reported by Git's post-rewrite hook.
type Rewrite struct {
	Old string `json:"old"`
	New string `json:"new"`
}

type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}
type Event struct {
	Kind            Kind       `json:"kind"`
	Agent           string     `json:"agent"`
	AgentSessionID  string     `json:"agent_session_id,omitempty"`
	Worktree        string     `json:"worktree"`
	At              int64      `json:"at"`
	Prompt          string     `json:"prompt,omitempty"`
	InitialTurnOnly bool       `json:"initial_turn_only,omitempty"`
	ToolName        string     `json:"tool_name,omitempty"`
	ContextType     string     `json:"context_type,omitempty"`
	Text            string     `json:"text,omitempty"`
	Paths           []string   `json:"paths,omitempty"`
	Lines           *LineRange `json:"lines,omitempty"`
	TranscriptPath  string     `json:"transcript_path,omitempty"`
	GitSHA          string     `json:"git_sha,omitempty"`
	// GitPrevious and GitAction come from the reflog entry for GitSHA (for
	// example "commit (amend)" or "rebase (pick)"); GitSources names the
	// commits a sequencer was copying. For git_rewrite, GitAction is the
	// rewrite type ("amend" or "rebase") and GitRewrites its pairs.
	GitPrevious string          `json:"git_previous,omitempty"`
	GitAction   string          `json:"git_action,omitempty"`
	GitSources  []string        `json:"git_sources,omitempty"`
	GitRewrites []Rewrite       `json:"git_rewrites,omitempty"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}

// gitActions are the reflog actions and rewrite types Trace understands.
var gitActions = map[string]bool{
	"commit": true, "commit (amend)": true, "commit (merge)": true, "commit (initial)": true,
	"cherry-pick": true, "amend": true, "rebase": true,
	"rebase (pick)": true, "rebase (reword)": true, "rebase (edit)": true, "rebase (squash)": true, "rebase (fixup)": true,
	"rebase -i (pick)": true, "rebase -i (reword)": true, "rebase -i (edit)": true, "rebase -i (squash)": true, "rebase -i (fixup)": true,
}

// ValidSHA reports whether s is a full SHA-1 or SHA-256 object name.
func ValidSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (e Event) Sanitized() Event {
	r := redact.Redactor{}
	if r.Text(e.AgentSessionID) != e.AgentSessionID {
		sum := sha256.Sum256([]byte(e.AgentSessionID))
		e.AgentSessionID = fmt.Sprintf("redacted-session-%x", sum)
	}
	e.Agent = r.Text(e.Agent)
	e.Worktree = r.Text(e.Worktree)
	e.ContextType = r.Text(e.ContextType)
	e.GitSHA = r.Text(e.GitSHA)
	if !ValidSHA(e.GitPrevious) {
		e.GitPrevious = ""
	}
	if !gitActions[e.GitAction] {
		e.GitAction = ""
	}
	sources := e.GitSources[:0:0]
	for _, sha := range e.GitSources {
		if ValidSHA(sha) {
			sources = append(sources, sha)
		}
	}
	e.GitSources = sources
	rewrites := e.GitRewrites[:0:0]
	for _, rewrite := range e.GitRewrites {
		if ValidSHA(rewrite.Old) && ValidSHA(rewrite.New) {
			rewrites = append(rewrites, rewrite)
		}
	}
	e.GitRewrites = rewrites
	e.Prompt = r.Text(e.Prompt)
	e.ToolName = r.Text(e.ToolName)
	e.Text = r.Text(e.Text)
	e.Raw = nil
	paths := make([]string, 0, len(e.Paths))
	for _, path := range e.Paths {
		if !manifest.SecretPath(path) && r.Text(path) == path {
			paths = append(paths, path)
		}
	}
	e.Paths = paths
	if manifest.SecretPath(e.TranscriptPath) || r.Text(e.TranscriptPath) != e.TranscriptPath {
		e.TranscriptPath = ""
	}
	if e.At == 0 {
		e.At = time.Now().UnixMilli()
	}
	return e
}
