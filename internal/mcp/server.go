package mcpserver

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/historysearch"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/restore"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
	"github.com/UseTrace-SI/trace-cli/internal/why"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Step struct {
	Step         int64  `json:"step"`
	Agent        string `json:"agent"`
	Prompt       string `json:"prompt"`
	FilesChanged int    `json:"files_changed,omitempty"`
}
type ListInput struct {
	Limit   int    `json:"limit,omitempty"`
	Session string `json:"session,omitempty"`
	Path    string `json:"path,omitempty"`
	Cursor  int64  `json:"cursor,omitempty"`
}
type ListOutput struct {
	Steps  []Step `json:"steps"`
	Cursor int64  `json:"cursor,omitempty"`
}
type TurnInput struct {
	Step int64 `json:"step"`
}
type TurnOutput struct {
	Step      int64    `json:"step"`
	Agent     string   `json:"agent"`
	Prompt    string   `json:"prompt"`
	Reasoning string   `json:"reasoning"`
	ToolCalls string   `json:"tool_calls"`
	Files     []string `json:"files"`
}
type ContextInput struct {
	Path  string `json:"path"`
	Limit int    `json:"limit,omitempty"`
}
type ContextOutput struct {
	Steps []Step `json:"steps"`
}
type WhyInput struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}
type WhyOutput struct {
	Step    int64  `json:"step,omitempty"`
	Agent   string `json:"agent"`
	Prompt  string `json:"prompt,omitempty"`
	Message string `json:"message,omitempty"`
}
type SearchInput struct {
	Query      string `json:"query,omitempty"`
	Scope      string `json:"scope,omitempty"`
	EditedPath string `json:"edited_path,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Cursor     int64  `json:"cursor,omitempty"`
}
type SearchOutput struct {
	Steps  []Step `json:"steps"`
	Cursor int64  `json:"cursor,omitempty"`
}
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	At      int64  `json:"at"`
}
type MessagesInput struct {
	Session string `json:"session,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}
type MessagesOutput struct {
	Messages []Message `json:"messages"`
}

func capLimit(n int) int {
	if n <= 0 {
		return 20
	}
	if n > 50 {
		return 50
	}
	return n
}

func New(e *engine.Engine) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "trace", Version: "0.1.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "list_checkpoints", Description: "List local Trace checkpoints"}, func(_ context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, ListOutput, error) {
		limit := capLimit(in.Limit)
		cursor := in.Cursor
		if cursor <= 0 {
			cursor = math.MaxInt64
		}
		rows, err := e.DB.Query(`SELECT k.seq,COALESCE(s.agent,''),COALESCE(t.prompt,''),
			(SELECT COUNT(DISTINCT o.path) FROM ops o WHERE o.turn_id=t.id AND o.path!='')
			FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id LEFT JOIN sessions s ON s.id=t.session_id
			WHERE k.seq<? AND (?='' OR s.id=?) AND (?='' OR EXISTS(SELECT 1 FROM ops o WHERE o.turn_id=t.id AND o.path=?))
			ORDER BY k.seq DESC LIMIT ?`, cursor, in.Session, in.Session, in.Path, in.Path, limit)
		if err != nil {
			return nil, ListOutput{}, err
		}
		defer rows.Close()
		out := ListOutput{Steps: []Step{}}
		for rows.Next() {
			var r Step
			if err := rows.Scan(&r.Step, &r.Agent, &r.Prompt, &r.FilesChanged); err != nil {
				return nil, out, err
			}
			if len(r.Prompt) > 120 {
				r.Prompt = textutil.Truncate(r.Prompt, 120)
			}
			out.Steps = append(out.Steps, r)
			out.Cursor = r.Step
		}
		return nil, out, rows.Err()
	})
	mcp.AddTool(s, &mcp.Tool{Name: "get_turn", Description: "Get a turn and changed files"}, func(_ context.Context, _ *mcp.CallToolRequest, in TurnInput) (*mcp.CallToolResult, TurnOutput, error) {
		out := TurnOutput{Step: in.Step}
		err := e.DB.QueryRow(`SELECT COALESCE(s.agent,''),COALESCE(t.prompt,''),COALESCE(t.reasoning,''),COALESCE(t.tool_calls,'')
			FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id LEFT JOIN sessions s ON s.id=t.session_id WHERE k.seq=?`, in.Step).Scan(&out.Agent, &out.Prompt, &out.Reasoning, &out.ToolCalls)
		if err != nil {
			return nil, out, err
		}
		current, err := e.ManifestAt(in.Step)
		if err != nil {
			return nil, out, err
		}
		previous := manifest.Manifest{}
		var prev int64
		if err := e.DB.QueryRow("SELECT seq FROM keyframes WHERE seq<? ORDER BY seq DESC LIMIT 1", in.Step).Scan(&prev); err == nil {
			previous, err = e.ManifestAt(prev)
			if err != nil {
				return nil, out, err
			}
		} else if err != sql.ErrNoRows {
			return nil, out, err
		}
		out.Files = restore.Plan(previous, current)
		if len(out.Reasoning) > 4000 {
			out.Reasoning = textutil.Truncate(out.Reasoning, 4000)
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "get_context", Description: "Get recent turns touching a path"}, func(_ context.Context, _ *mcp.CallToolRequest, in ContextInput) (*mcp.CallToolResult, ContextOutput, error) {
		rows, err := e.DB.Query(`SELECT DISTINCT k.seq,COALESCE(s.agent,''),COALESCE(t.prompt,'')
			FROM ops o JOIN turns t ON t.id=o.turn_id JOIN keyframes k ON k.turn_id=t.id
			JOIN sessions s ON s.id=t.session_id WHERE o.path=? ORDER BY k.seq DESC LIMIT ?`, in.Path, capLimit(in.Limit))
		if err != nil {
			return nil, ContextOutput{}, err
		}
		defer rows.Close()
		out := ContextOutput{Steps: []Step{}}
		for rows.Next() {
			var r Step
			if err := rows.Scan(&r.Step, &r.Agent, &r.Prompt); err != nil {
				return nil, out, err
			}
			out.Steps = append(out.Steps, r)
		}
		return nil, out, rows.Err()
	})
	mcp.AddTool(s, &mcp.Tool{Name: "why_line", Description: "Find the edit responsible for a line"}, func(_ context.Context, _ *mcp.CallToolRequest, in WhyInput) (*mcp.CallToolResult, WhyOutput, error) {
		if in.Line < 1 {
			return nil, WhyOutput{}, fmt.Errorf("line must be positive")
		}
		rows, err := e.DB.Query(`SELECT COALESCE(o.before_hash,''),COALESCE(o.after_hash,''),COALESCE(o.turn_id,'') FROM ops o
			WHERE o.path=? AND (o.before_hash IS NOT NULL OR o.after_hash IS NOT NULL) ORDER BY o.at DESC,o.id DESC`, in.Path)
		if err != nil {
			return nil, WhyOutput{}, err
		}
		type change struct{ before, after, tid string }
		var changes []change
		for rows.Next() {
			var ch change
			if err := rows.Scan(&ch.before, &ch.after, &ch.tid); err != nil {
				rows.Close()
				return nil, WhyOutput{}, err
			}
			changes = append(changes, ch)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, WhyOutput{}, err
		}
		if err := rows.Close(); err != nil {
			return nil, WhyOutput{}, err
		}
		line := in.Line
		for i := 0; i < len(changes); {
			ch := changes[i]
			i++
			for i < len(changes) && changes[i].before == ch.before && changes[i].after == ch.after && changes[i].tid == ch.tid {
				i++
			}
			before, after, tid := ch.before, ch.after, ch.tid
			oldText, oldLarge, err := readBlob(e, blob.Hash(before))
			if err != nil {
				return nil, WhyOutput{}, err
			}
			newText, newLarge, err := readBlob(e, blob.Hash(after))
			if err != nil {
				return nil, WhyOutput{}, err
			}
			if oldLarge || newLarge {
				return nil, WhyOutput{Agent: "unknown", Message: "origin uncertain after a large edit"}, nil
			}
			oldLine, touched := why.MapLine(oldText, newText, line)
			if oldLine < 0 {
				return nil, WhyOutput{Agent: "unknown", Message: "origin uncertain after a large edit"}, nil
			}
			if touched {
				if tid == "" {
					return nil, WhyOutput{Agent: "human", Message: "edited by you"}, nil
				}
				var out WhyOutput
				err := e.DB.QueryRow(`SELECT k.seq,s.agent,COALESCE(t.prompt,'') FROM turns t JOIN sessions s ON s.id=t.session_id JOIN keyframes k ON k.turn_id=t.id WHERE t.id=?`, tid).Scan(&out.Step, &out.Agent, &out.Prompt)
				return nil, out, err
			}
			line = oldLine
		}
		return nil, WhyOutput{Agent: "baseline", Message: "present at baseline"}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "search_history", Description: "Search turn text, messages, and edited paths"}, func(_ context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
		hits, err := historysearch.Search(e.DB.DB, historysearch.Options{
			Query: in.Query, Scope: in.Scope, EditedPath: in.EditedPath, Limit: capLimit(in.Limit), Before: in.Cursor,
		})
		if err != nil {
			return nil, SearchOutput{}, err
		}
		out := SearchOutput{Steps: []Step{}}
		for _, hit := range hits {
			r := Step{Step: hit.Step, Agent: hit.Agent, Prompt: hit.Prompt}
			if len(r.Prompt) > 120 {
				r.Prompt = textutil.Truncate(r.Prompt, 120)
			}
			out.Steps = append(out.Steps, r)
			out.Cursor = r.Step
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "get_messages", Description: "Get conversation messages for a session"}, func(_ context.Context, _ *mcp.CallToolRequest, in MessagesInput) (*mcp.CallToolResult, MessagesOutput, error) {
		limit := capLimit(in.Limit)
		rows, err := e.DB.Query(`SELECT m.role, COALESCE(m.content,''), m.at FROM messages m WHERE (?='' OR m.session_id=?) ORDER BY m.at LIMIT ?`, in.Session, in.Session, limit)
		if err != nil {
			return nil, MessagesOutput{}, err
		}
		defer rows.Close()
		out := MessagesOutput{Messages: []Message{}}
		for rows.Next() {
			var m Message
			if err := rows.Scan(&m.Role, &m.Content, &m.At); err != nil {
				return nil, out, err
			}
			out.Messages = append(out.Messages, m)
		}
		return nil, out, rows.Err()
	})
	return s
}

func readBlob(e *engine.Engine, h blob.Hash) (string, bool, error) {
	if h == "" {
		return "", false, nil
	}
	r, err := e.Blobs.Get(h)
	if err != nil {
		return "", false, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return "", false, err
	}
	if len(b) > 1<<20 {
		return "", true, nil
	}
	return strings.ToValidUTF8(string(b), ""), false, nil
}
