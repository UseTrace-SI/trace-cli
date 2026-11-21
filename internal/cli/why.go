package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/attribution"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/spf13/cobra"
)

type whyResult struct {
	Step      int64  `json:"step,omitempty"`
	Agent     string `json:"agent"`
	Time      int64  `json:"time"`
	Prompt    string `json:"prompt,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	// Commit is the commit that currently contains the line; OriginCommit
	// is where it was first committed when Git rewrites moved it since.
	Commit       string `json:"commit,omitempty"`
	OriginCommit string `json:"origin_commit,omitempty"`
	Symbol       string `json:"symbol,omitempty"`
	Message      string `json:"message,omitempty"`
	// Confidence is exact, normalized (matched ignoring formatting),
	// inferred (matched by content to another commit) or uncertain.
	Confidence   string                `json:"confidence"`
	RewriteChain []attribution.Rewrite `json:"rewrite_chain,omitempty"`
	Messages     []whyMessage          `json:"messages,omitempty"`
	MoreMessages bool                  `json:"more_messages,omitempty"`
}

func newWhyResult(origin attribution.Origin) whyResult {
	return whyResult{Step: origin.Step, Agent: origin.Agent, Time: origin.Time, Prompt: origin.Prompt,
		Reasoning: origin.Reasoning, Commit: origin.Commit, OriginCommit: origin.OriginCommit, Symbol: origin.Symbol,
		Message: origin.Message, Confidence: string(origin.Confidence), RewriteChain: origin.Rewrites}
}

type whyMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	At      int64  `json:"at"`
}

func whyCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var all bool
	var messages bool
	cmd := &cobra.Command{Use: "why <file>:<line>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		pos := strings.LastIndex(args[0], ":")
		if pos < 0 {
			return fmt.Errorf("expected file:line")
		}
		line, err := strconv.Atoi(args[0][pos+1:])
		if err != nil || line < 1 {
			return fmt.Errorf("invalid line number")
		}
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		file, err := repo.RelativePath(root, args[0][:pos])
		if err != nil || manifest.Excluded(file) {
			return fmt.Errorf("unsafe file path")
		}
		current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		if line > len(strings.Split(strings.TrimSuffix(string(current), "\n"), "\n")) {
			return fmt.Errorf("line is outside the file")
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		origins, err := attribution.ExplainCommitted(e.DB, e.Blobs, root, file, string(current), line, all)
		if err != nil {
			return err
		}
		results := make([]whyResult, 0, len(origins))
		for _, origin := range origins {
			results = append(results, newWhyResult(origin))
		}
		if messages {
			for i := range results {
				if results[i].Step < 1 {
					continue
				}
				var err error
				results[i].Messages, results[i].MoreMessages, err = whyMessages(e, results[i].Step)
				if err != nil {
					return err
				}
			}
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(results)
		}
		for _, r := range results {
			if r.Symbol != "" {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "Symbol: %s\n", r.Symbol); err != nil {
					return err
				}
			}
			if err := printProvenance(c.OutOrStdout(), r); err != nil {
				return err
			}
			if r.Step > 0 {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "Step %d: %s — %s\n", r.Step, r.Agent, r.Prompt); err != nil {
					return err
				}
				for _, message := range r.Messages {
					if _, err := fmt.Fprintf(c.OutOrStdout(), "  [%s] %s\n", message.Role, message.Content); err != nil {
						return err
					}
				}
				if r.MoreMessages {
					if _, err := fmt.Fprintf(c.OutOrStdout(), "  More: trace messages --step %d --offset 20\n", r.Step); err != nil {
						return err
					}
				}
			} else {
				if _, err := fmt.Fprintln(c.OutOrStdout(), r.Message); err != nil {
					return err
				}
			}
		}
		return nil
	}}
	cmd.Flags().BoolVar(&all, "all", false, "show all matching edits")
	cmd.Flags().BoolVar(&messages, "messages", false, "include up to 20 messages from the matching turn")
	return cmd
}

func whyMessages(e *engine.Engine, step int64) ([]whyMessage, bool, error) {
	rows, err := e.DB.Query(`SELECT m.role,COALESCE(m.content,''),m.at FROM messages m
		JOIN keyframes k ON k.turn_id=m.turn_id WHERE k.seq=? ORDER BY m.at,m.rowid LIMIT 21`, step)
	if err != nil {
		return nil, false, fmt.Errorf("finding step messages: %w", err)
	}
	defer rows.Close()
	messages := make([]whyMessage, 0)
	for rows.Next() {
		var message whyMessage
		if err := rows.Scan(&message.Role, &message.Content, &message.At); err != nil {
			return nil, false, fmt.Errorf("reading step message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("reading step messages: %w", err)
	}
	if len(messages) > 20 {
		return messages[:20], true, nil
	}
	return messages, false, nil
}

// printProvenance reports where a committed line has been since it was
// written: its commit, the rewrites that carried it there and how closely it
// matched.
func printProvenance(out io.Writer, r whyResult) error {
	if r.Commit != "" {
		line := "Commit: " + shortSHA(r.Commit)
		if len(r.RewriteChain) > 0 {
			chain := shortSHA(r.RewriteChain[0].From)
			for _, rewrite := range r.RewriteChain {
				chain += " -> " + shortSHA(rewrite.To) + " (" + rewrite.Kind + ")"
			}
			line += ", rewritten " + chain
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	if r.Confidence != "" && r.Confidence != string(attribution.Exact) && r.Confidence != string(attribution.Uncertain) {
		_, err := fmt.Fprintf(out, "Confidence: %s\n", r.Confidence)
		return err
	}
	return nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
