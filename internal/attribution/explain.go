// Package attribution explains which recorded edit produced a line of a file.
// It is shared by `trace why` and the read-only MCP server so both apply the
// same alignment between the current file and recorded history.
package attribution

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

// MaxTextBytes bounds every file state read while explaining a line.
const MaxTextBytes = 1 << 20

// Origin describes one recorded edit, or the reason no edit can be named.
type Origin struct {
	Step       int64
	Agent      string
	Time       int64
	Prompt     string
	Reasoning  string
	Commit     string
	Symbol     string
	Message    string
	TurnID     string
	Confidence Confidence
	// OriginCommit is the commit in which the line was first committed when it
	// differs from Commit; Rewrites lead from OriginCommit to Commit.
	OriginCommit string
	Rewrites     []Rewrite
}

// Rewrite is one recorded Git rewrite that carried a line to a new commit.
type Rewrite struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// Querier is the database surface used to read history.
type Querier interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

// ResolvePath validates a user-supplied path and returns it relative to root.
// Paths outside the repository and secret or excluded paths are rejected so
// that their contents are never read.
func ResolvePath(root, path string) (string, error) {
	file, err := repo.RelativePath(root, path)
	if err != nil || manifest.Excluded(file) {
		return "", errors.New("unsafe file path")
	}
	return file, nil
}

// ExplainFile reads the current working copy of file (relative to root) and
// explains the given 1-based line.
func ExplainFile(db Querier, blobs *blob.Store, root, file string, line int, all bool) ([]Origin, error) {
	if line < 1 {
		return nil, errors.New("invalid line number")
	}
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxTextBytes+1))
	closeErr := f.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	if len(b) > MaxTextBytes {
		return []Origin{{Agent: "unknown", Message: "origin uncertain after a large edit"}}, nil
	}
	current := string(b)
	if line > len(strings.Split(strings.TrimSuffix(current, "\n"), "\n")) {
		return nil, errors.New("line is outside the file")
	}
	return ExplainCommitted(db, blobs, root, file, current, line, all)
}

// Explain maps a line of current, the working text of file, back through the
// recorded file states. The working file may have changed since the latest
// capture, so it is aligned with each recorded state before stored line ranges
// are used; an uncaptured line is never attributed to an agent.
func Explain(db Querier, blobs *blob.Store, file, current string, line int, all bool) ([]Origin, error) {
	w := newWalker(db, blobs)
	verdicts, err := w.walk(file, current, []int{line}, 0, all)
	if err != nil {
		return nil, err
	}
	return w.origins(verdicts[0], all)
}

// ExplainCommitted explains a line of the working copy of file in a Git
// repository, following committed lines through `git blame` and recorded
// rewrites (see ExplainLines).
func ExplainCommitted(db Querier, blobs *blob.Store, root, file, current string, line int, all bool) ([]Origin, error) {
	origins, err := ExplainLines(db, blobs, root, file, current, []int{line}, all)
	if err != nil {
		return nil, err
	}
	return origins[0], nil
}

// origins expands a verdict, and with all its formatting-only edits, into
// reported origins with turn details.
func (w *walker) origins(v verdict, all bool) ([]Origin, error) {
	var out []Origin
	if all {
		for _, edit := range v.trail {
			origin, err := w.origin(edit)
			if err != nil {
				return nil, err
			}
			out = append(out, origin)
		}
	}
	origin, err := w.origin(v)
	if err != nil {
		return nil, err
	}
	return append(out, origin), nil
}

func (w *walker) origin(v verdict) (Origin, error) {
	o := Origin{Time: v.at, Symbol: v.symbol, Message: v.message, Confidence: v.confidence, TurnID: v.turnID,
		Commit: v.commit, Rewrites: v.rewrites}
	if v.origin != v.commit {
		o.OriginCommit = v.origin
	}
	switch v.kind {
	case "human":
		o.Agent = "human"
	case "baseline":
		o.Agent = "baseline"
	case "unknown":
		o.Agent = "unknown"
		o.Confidence = Uncertain
	case "turn":
		t, err := w.turn(v.turnID)
		o.Step, o.Agent, o.Prompt, o.Reasoning = t.step, t.agent, t.prompt, t.reasoning
		commit := t.commit
		if err == sql.ErrNoRows {
			o.Agent = v.agent
			if o.Agent == "" {
				o.Agent = "unknown"
			}
			if o.Message == "" {
				o.Message = "turn has no keyframe yet"
			}
		} else if err != nil {
			return Origin{}, err
		}
		if o.Commit == "" {
			o.Commit = commit
		}
		if len(o.Reasoning) > 400 {
			o.Reasoning = textutil.Truncate(o.Reasoning, 400)
		}
	}
	return o, nil
}

func textLimited(bs *blob.Store, h blob.Hash) (string, bool, error) {
	if h == "" {
		return "", false, nil
	}
	r, err := bs.Get(h)
	if err != nil {
		return "", false, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, MaxTextBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(b) > MaxTextBytes {
		return textutil.TruncateBytes(string(b), MaxTextBytes), true, nil
	}
	return string(b), false, nil
}

type turnDetails struct {
	step                             int64
	agent, prompt, reasoning, commit string
	err                              error
}

func (w *walker) turn(id string) (turnDetails, error) {
	if w.turns == nil {
		w.turns = map[string]turnDetails{}
	}
	if t, ok := w.turns[id]; ok {
		return t, t.err
	}
	var t turnDetails
	t.err = w.db.QueryRow(`SELECT COALESCE(k.seq,0),s.agent,COALESCE(t.prompt,''),COALESCE(t.reasoning,''),COALESCE(c.sha,'')
		FROM turns t JOIN sessions s ON s.id=t.session_id LEFT JOIN keyframes k ON k.turn_id=t.id
		LEFT JOIN commits c ON c.keyframe_id=k.id WHERE t.id=?`, id).Scan(&t.step, &t.agent, &t.prompt, &t.reasoning, &t.commit)
	w.turns[id] = t
	return t, t.err
}
