package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	traceexport "github.com/UseTrace-SI/trace-cli/internal/export"
	"github.com/spf13/cobra"
)

func exportCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var format, out string
	var publish, includeExcluded bool
	cmd := &cobra.Command{Use: "export", Short: "Export code attribution", RunE: func(c *cobra.Command, _ []string) error {
		if format == "bundle" {
			root, err := initializedRoot(*repoPath)
			if err != nil {
				return err
			}
			if out == "" {
				return fmt.Errorf("--out is required")
			}
			excluded, err := exportBundle(root, out, bundleOptions{IncludeExcludedHistory: includeExcluded})
			if err != nil {
				return err
			}
			if *jsonOutput {
				if err := reportExcludedHistory(nil, c.ErrOrStderr(), excluded, includeExcluded); err != nil {
					return err
				}
				return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"path": out, "format": "bundle", "historical_paths_now_excluded": nonNil(excluded), "excluded_history_included": includeExcluded})
			}
			if _, err := fmt.Fprintf(c.OutOrStdout(), "Exported bundle to %s\n", out); err != nil {
				return err
			}
			return reportExcludedHistory(c.OutOrStdout(), c.ErrOrStderr(), excluded, includeExcluded)
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
	cmd.Flags().BoolVar(&includeExcluded, "include-excluded-history", false, "bundle: keep snapshots of since-excluded credential files instead of leaving them out")
	return cmd
}

func nonNil(paths []string) []string {
	if paths == nil {
		return []string{}
	}
	return paths
}

// reportExcludedHistory lists the since-excluded paths a bundle left out on
// stdout (skipped when stdout is nil, for JSON output) or, when asked to keep
// them, always warns on stderr that the bundle contains them.
func reportExcludedHistory(stdout, stderr io.Writer, paths []string, included bool) error {
	if len(paths) == 0 {
		return nil
	}
	if included {
		_, err := fmt.Fprintf(stderr, "warning: the bundle includes earlier snapshots of %d excluded path(s), which may hold credentials: %s\n", len(paths), strings.Join(paths, ", "))
		return err
	}
	if stdout == nil {
		return nil
	}
	_, err := fmt.Fprintf(stdout, "Left out earlier snapshots of %d excluded path(s): %s (keep them with --include-excluded-history)\n", len(paths), strings.Join(paths, ", "))
	return err
}
