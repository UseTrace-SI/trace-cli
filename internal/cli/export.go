package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	traceexport "github.com/UseTrace-SI/trace-cli/internal/export"
	"github.com/spf13/cobra"
)

func exportCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var format, out string
	cmd := &cobra.Command{Use: "export", Short: "Export code attribution", RunE: func(c *cobra.Command, _ []string) error {
		if format != "agent-trace" {
			return fmt.Errorf("unsupported export format %q", format)
		}
		if out == "" {
			return fmt.Errorf("--out is required")
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
		dir := filepath.Dir(out)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(dir, ".trace-export-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if err := tmp.Chmod(0600); err != nil {
			tmp.Close()
			return err
		}
		count, err := traceexport.AgentTrace(e.DB, tmp)
		if err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), out); err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"path": out, "records": count})
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Exported %d records to %s\n", count, out)
		return err
	}}
	cmd.Flags().StringVar(&format, "format", "agent-trace", "export format")
	cmd.Flags().StringVar(&out, "out", "", "output path")
	return cmd
}
