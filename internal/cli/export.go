package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	traceexport "github.com/UseTrace-SI/trace-cli/internal/export"
	"github.com/spf13/cobra"
)

func exportCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var format, out string
	var publish bool
	cmd := &cobra.Command{Use: "export", Short: "Export code attribution", RunE: func(c *cobra.Command, _ []string) error {
		if format == "bundle" {
			root, err := initializedRoot(*repoPath)
			if err != nil {
				return err
			}
			if out == "" {
				return fmt.Errorf("--out is required")
			}
			if err := writeBundle(root, out); err != nil {
				return err
			}
			if *jsonOutput {
				return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"path": out, "format": "bundle"})
			}
			_, err = fmt.Fprintf(c.OutOrStdout(), "Exported bundle to %s\n", out)
			return err
		}
		if format != "agent-trace" {
			return fmt.Errorf("unsupported export format %q", format)
		}
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if publish && out == "" {
			out = filepath.Join(root, "trace-history", time.Now().UTC().Format("20060102T150405Z")+".jsonl")
		}
		if out == "" {
			return fmt.Errorf("--out is required (or use --publish)")
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
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"path": out, "records": count, "publish": publish})
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Exported %d records to %s\n", count, out)
		if err != nil {
			return err
		}
		if publish {
			_, err = fmt.Fprintf(c.OutOrStdout(), "Publish with: git add %s && git commit -m \"trace: session history\"\n", out)
		}
		return err
	}}
	cmd.Flags().StringVar(&format, "format", "agent-trace", "export format")
	cmd.Flags().StringVar(&out, "out", "", "output path")
	cmd.Flags().BoolVar(&publish, "publish", false, "write a commit-ready (redacted) session log under trace-history/")
	return cmd
}
