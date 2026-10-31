package mcpserver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/companions"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/historysearch"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

// GetContext is shared by the CLI and MCP. Lookup never refreshes indexes,
// creates companion databases, or invokes an extraction agent.
func GetContext(ctx context.Context, e *engine.Engine, in ContextInput) (ContextOutput, error) {
	out := ContextOutput{Steps: []Step{}}
	if len(in.Query) > 4096 || len(in.Path) > 4096 {
		return out, fmt.Errorf("context path or query exceeds 4096 bytes")
	}
	if in.Path != "" {
		in.Path = filepath.ToSlash(filepath.Clean(in.Path))
		if filepath.IsAbs(in.Path) || in.Path == ".." || strings.HasPrefix(in.Path, "../") || manifest.Excluded(in.Path) {
			return out, fmt.Errorf("context path must be a non-excluded repository-relative path")
		}
	}
	if in.Path == "" && strings.TrimSpace(in.Query) == "" {
		return out, fmt.Errorf("provide a path or task query")
	}
	if in.Path != "" {
		rows, err := e.DB.QueryContext(ctx, `SELECT DISTINCT k.seq,COALESCE(s.agent,''),substr(COALESCE(t.prompt,''),1,4000)
			FROM ops o JOIN turns t ON t.id=o.turn_id JOIN keyframes k ON k.turn_id=t.id
			JOIN sessions s ON s.id=t.session_id WHERE o.path=? ORDER BY k.seq DESC LIMIT ?`, in.Path, capLimit(in.Limit))
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var item Step
			if err := rows.Scan(&item.Step, &item.Agent, &item.Prompt); err != nil {
				rows.Close()
				return out, err
			}
			item.Prompt = e.Redactor.Text(item.Prompt)
			out.Steps = append(out.Steps, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	} else {
		words := strings.Fields(in.Query)
		if len(words) > 16 {
			words = words[:16]
		}
		hits, err := historysearch.Search(e.DB.DB, historysearch.Options{Query: strings.Join(words, " "), Limit: capLimit(in.Limit), PromptLimit: 4000, OmitSnippets: true})
		if err != nil {
			return out, err
		}
		for _, hit := range hits {
			out.Steps = append(out.Steps, Step{Step: hit.Step, Agent: hit.Agent, Prompt: e.Redactor.Text(hit.Prompt)})
		}
	}
	query := in.Query
	if query == "" {
		query = filepath.Base(in.Path)
	}
	out.Brain, out.Graph = companions.Lookup(ctx, e.Root, in.Path, query, capLimit(in.Limit))
	// Imported history is already redacted. Apply the same boundary to manual
	// memories and source signatures before returning them to a coding agent.
	redact := e.Redactor.Text
	for i := range out.Brain.Memories {
		m := &out.Brain.Memories[i]
		m.ID, m.Scope, m.Content, m.Source = redact(m.ID), redact(m.Scope), redact(m.Content), redact(m.Source)
		m.SessionID, m.TurnID = redact(m.SessionID), redact(m.TurnID)
	}
	for i := range out.Graph.Symbols {
		s := &out.Graph.Symbols[i]
		s.ID, s.File, s.Name, s.Kind, s.Signature = redact(s.ID), redact(s.File), redact(s.Name), redact(s.Kind), redact(s.Signature)
	}
	for i := range out.Graph.Relations {
		r := &out.Graph.Relations[i]
		r.From, r.To, r.Kind, r.File = redact(r.From), redact(r.To), redact(r.Kind), redact(r.File)
	}
	return out, nil
}
