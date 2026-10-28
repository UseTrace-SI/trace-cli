package cli

import (
	"encoding/json"
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func gcCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var days int
	var dryRun bool
	cmd := &cobra.Command{Use: "gc", Short: "Remove expired local history", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		result, err := e.GC(days, dryRun)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(result)
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Removed %d keyframes and %d blobs\n", result.RemovedKeyframes, result.RemovedBlobs)
		return err
	}}
	cmd.Flags().IntVar(&days, "keep-days", 30, "retain history for this many days")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show planned deletion")
	return cmd
}
