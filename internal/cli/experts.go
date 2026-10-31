package cli

import (
	"encoding/json"
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func expertsCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var path string
	cmd := &cobra.Command{Use: "experts", Short: "Rank agent provenance for code scopes", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		type expert struct {
			Agent   string `json:"agent"`
			Ops     int64  `json:"ops"`
			Files   int64  `json:"files"`
		}
		q := `SELECT COALESCE(s.agent,''), COUNT(*), COUNT(DISTINCT o.path) FROM ops o LEFT JOIN turns t ON t.id=o.turn_id LEFT JOIN sessions s ON s.id=t.session_id WHERE o.path<>''`
		var args []any
		if path != "" {
			q += " AND o.path LIKE ?"
			args = append(args, path+"%")
		}
		q += " GROUP BY s.agent ORDER BY COUNT(*) DESC"
		rows, err := e.DB.Query(q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		var out []expert
		for rows.Next() {
			var ex expert
			if err := rows.Scan(&ex.Agent, &ex.Ops, &ex.Files); err != nil {
				return err
			}
			out = append(out, ex)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(out)
		}
		fmt.Fprintf(c.OutOrStdout(), "Agent provenance for %s\n", root)
		for _, ex := range out {
			fmt.Fprintf(c.OutOrStdout(), "  %-16s %d ops across %d files\n", ex.Agent, ex.Ops, ex.Files)
		}
		return nil
	}}
	cmd.Flags().StringVar(&path, "path", "", "limit to a path prefix")
	return cmd
}