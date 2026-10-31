package cli

import (
	"encoding/json"
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func timelineCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "timeline", Short: "Show fine-grained edit timeline", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		type entry struct {
			At     int64  `json:"at"`
			Path   string `json:"path"`
			Kind   string `json:"kind"`
			Source string `json:"source"`
			Turn   int64  `json:"turn,omitempty"`
			Agent  string `json:"agent,omitempty"`
		}
		rows, err := e.DB.Query(`SELECT o.at,o.path,o.kind,o.source,COALESCE(t.seq,0),COALESCE(s.agent,'')
			FROM ops o LEFT JOIN turns t ON t.id=o.turn_id LEFT JOIN sessions s ON s.id=t.session_id
			ORDER BY o.at ASC, o.id ASC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var out []entry
		for rows.Next() {
			var en entry
			if err := rows.Scan(&en.At, &en.Path, &en.Kind, &en.Source, &en.Turn, &en.Agent); err != nil {
				return err
			}
			out = append(out, en)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(out)
		}
		for _, en := range out {
			fmt.Fprintf(c.OutOrStdout(), "%d  %-8s %-6s %s %s\n", en.At, en.Source, en.Kind, en.Path, en.Agent)
		}
		return nil
	}}
	return cmd
}