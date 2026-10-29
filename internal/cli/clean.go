package cli

import (
	"encoding/json"
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func cleanCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var force bool
	cmd := &cobra.Command{Use: "clean", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		res, err := e.GC(0, !force)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(res)
		}
		if !force {
			_, err = fmt.Fprintf(c.OutOrStdout(), "Would remove %d unlinked keyframes. Run trace clean --force to apply.\n", res.RemovedKeyframes)
			return err
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Removed %d keyframes and %d blobs.\n", res.RemovedKeyframes, res.RemovedBlobs)
		return err
	}}
	cmd.Flags().BoolVar(&force, "force", false, "remove unlinked local history after preview")
	return cmd
}
