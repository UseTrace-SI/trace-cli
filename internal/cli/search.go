package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/historysearch"
	"github.com/spf13/cobra"
)

func searchCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var limit int
	var scope, editedPath string
	cmd := &cobra.Command{Use: "search [terms...]", RunE: func(c *cobra.Command, args []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		hits, err := historysearch.Search(e.DB.DB, historysearch.Options{
			Query: strings.Join(args, " "), Scope: scope, EditedPath: editedPath, Limit: limit,
		})
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(hits)
		}
		if len(hits) == 0 {
			_, err = fmt.Fprintln(c.OutOrStdout(), "No matches.")
			return err
		}
		for _, h := range hits {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "%d  %-12s  %s\n", h.Step, h.Agent, h.Prompt); err != nil {
				return err
			}
		}
		return nil
	}}
	cmd.Flags().IntVar(&limit, "limit", 20, "max results")
	cmd.Flags().StringVar(&scope, "scope", "all", "search all turn text or only turns or messages")
	cmd.Flags().StringVar(&editedPath, "edited-path", "", "only turns that edited a matching path (glob)")
	return cmd
}
