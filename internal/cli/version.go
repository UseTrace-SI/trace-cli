package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func versionCommand(version, commit, date string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{Use: "version", RunE: func(cmd *cobra.Command, _ []string) error {
		if *jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"version": version, "commit": commit, "date": date})
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "trace %s (%s, %s)\n", version, commit, date)
		return err
	}}
}
