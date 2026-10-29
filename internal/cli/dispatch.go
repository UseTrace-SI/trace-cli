package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func dispatchCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var limit int
	cmd := &cobra.Command{Use: "dispatch", RunE: func(c *cobra.Command, _ []string) error {
		if limit <= 0 {
			limit = 10
		}
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		rows, err := e.DB.Query(`SELECT k.seq,COALESCE(s.agent,''),COALESCE(t.prompt,''),COALESCE(t.reasoning,'')
			FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id LEFT JOIN sessions s ON s.id=t.session_id
			WHERE k.tag='turn' ORDER BY k.seq DESC LIMIT ?`, limit)
		if err != nil {
			return fmt.Errorf("querying history: %w", err)
		}
		defer rows.Close()
		type entry struct {
			Seq       int64  `json:"step"`
			Agent     string `json:"agent"`
			Prompt    string `json:"prompt"`
			Reasoning string `json:"reasoning,omitempty"`
		}
		entries := make([]entry, 0)
		for rows.Next() {
			var en entry
			if err := rows.Scan(&en.Seq, &en.Agent, &en.Prompt, &en.Reasoning); err != nil {
				return fmt.Errorf("reading history: %w", err)
			}
			entries = append(entries, en)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// reverse to chronological
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(entries)
		}
		var b strings.Builder
		b.WriteString("# Recent agent work\n\n")
		for _, en := range entries {
			b.WriteString(fmt.Sprintf("## Step %d — %s\n", en.Seq, en.Agent))
			b.WriteString(fmt.Sprintf("**Prompt:** %s\n\n", en.Prompt))
			if en.Reasoning != "" {
				excerpt := en.Reasoning
				if len(excerpt) > 400 {
					excerpt = excerpt[:400] + "…"
				}
				b.WriteString(fmt.Sprintf("**Reasoning:** %s\n\n", excerpt))
			}
		}
		_, err = fmt.Fprint(c.OutOrStdout(), b.String())
		return err
	}}
	cmd.Flags().IntVar(&limit, "limit", 10, "number of recent turns to summarize")
	return cmd
}
