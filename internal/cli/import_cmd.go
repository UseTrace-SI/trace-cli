package cli

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
	"github.com/UseTrace-SI/trace-cli/internal/companions"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func importCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var source, bundle string
	cmd := &cobra.Command{Use: "import", Short: "Import history from another Trace database", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if bundle != "" {
			if err := readBundle(root, bundle); err != nil {
				return err
			}
			report := companions.Refresh(c.Context(), root)
			_ = companions.SaveStatus(root, report)
			if *jsonOutput {
				return nil
			}
			fmt.Fprintf(c.OutOrStdout(), "imported bundle %s (brain: %s, graph: %s)\n", bundle, report.Brain.Status, report.Graph.Status)
			return nil
		}
		if source == "" {
			return fmt.Errorf("--from is required")
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		src, err := sql.Open("sqlite", "file:"+source+"?mode=ro")
		if err != nil {
			return err
		}
		defer src.Close()
		tables := []string{"sessions", "turns", "keyframes", "ops", "commits", "messages", "transcript_offsets"}
		for _, t := range tables {
			rows, err := src.Query("SELECT * FROM " + t)
			if err != nil {
				continue
			}
			cols, _ := rows.Columns()
			placeholders := ""
			for i := range cols {
				if i > 0 {
					placeholders += ","
				}
				placeholders += "?"
			}
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					continue
				}
				q := "INSERT OR IGNORE INTO " + t + " VALUES (" + placeholders + ")"
				e.DB.Exec(q, vals...)
			}
			rows.Close()
		}
		report := companions.Refresh(c.Context(), root)
		_ = companions.SaveStatus(root, report)
		if *jsonOutput {
			return nil
		}
		fmt.Fprintf(c.OutOrStdout(), "imported history from %s (brain: %s, graph: %s)\n", source, report.Brain.Status, report.Graph.Status)
		return nil
	}}
	cmd.Flags().StringVar(&source, "from", "", "source Trace database to import")
	cmd.Flags().StringVar(&bundle, "bundle", "", "bundle file to import")
	return cmd
}