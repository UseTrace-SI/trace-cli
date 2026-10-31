package cli

import (
	"encoding/json"
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func tokensCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "tokens", Short: "Analyze token usage across sessions and checkpoints", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		type row struct {
			SessionID string `json:"session_id"`
			Agent     string `json:"agent"`
			TokensIn  int64  `json:"tokens_in"`
			TokensOut int64  `json:"tokens_out"`
		}
		rows, err := e.DB.Query(`SELECT t.session_id, COALESCE(s.agent,''), COALESCE(SUM(t.tokens_in),0), COALESCE(SUM(t.tokens_out),0)
			FROM turns t LEFT JOIN sessions s ON s.id=t.session_id GROUP BY t.session_id ORDER BY t.session_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var out []row
		var totalIn, totalOut int64
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.SessionID, &r.Agent, &r.TokensIn, &r.TokensOut); err != nil {
				return err
			}
			out = append(out, r)
			totalIn += r.TokensIn
			totalOut += r.TokensOut
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"sessions": out, "total_in": totalIn, "total_out": totalOut})
		}
		fmt.Fprintf(c.OutOrStdout(), "Token usage for %s\n", root)
		for _, r := range out {
			fmt.Fprintf(c.OutOrStdout(), "  %-20s %-12s %d in / %d out\n", r.SessionID, r.Agent, r.TokensIn, r.TokensOut)
		}
		fmt.Fprintf(c.OutOrStdout(), "  total: %d in / %d out\n", totalIn, totalOut)
		return nil
	}}
	return cmd
}