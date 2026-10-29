package cli

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/spf13/cobra"
)

func diffCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var content bool = true
	cmd := &cobra.Command{Use: "diff <step-a> <step-b>", Args: cobra.ExactArgs(2), RunE: func(c *cobra.Command, args []string) error {
		a, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid step %q: %w", args[0], err)
		}
		b, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid step %q: %w", args[1], err)
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
		ma, err := e.ManifestAt(a)
		if err != nil {
			return fmt.Errorf("loading step %d: %w", a, err)
		}
		mb, err := e.ManifestAt(b)
		if err != nil {
			return fmt.Errorf("loading step %d: %w", b, err)
		}
		added, modified, deleted := manifest.Diff(ma, mb)
		result := map[string]any{
			"from":     a,
			"to":       b,
			"added":    added,
			"modified": modified,
			"deleted":  deleted,
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(result)
		}
		if len(added) == 0 && len(modified) == 0 && len(deleted) == 0 {
			_, err = fmt.Fprintf(c.OutOrStdout(), "No file changes between step %d and step %d.\n", a, b)
			return err
		}
		for _, p := range added {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "A  %s\n", p); err != nil {
				return err
			}
		}
		for _, p := range modified {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "M  %s\n", p); err != nil {
				return err
			}
			if content {
				oldText, _, err := blobTextLimited(e.Blobs, ma[p].Hash)
				if err != nil {
					return err
				}
				newText, _, err := blobTextLimited(e.Blobs, mb[p].Hash)
				if err != nil {
					return err
				}
				if d := unifiedDiff(p, oldText, newText); d != "" {
					if _, err := fmt.Fprint(c.OutOrStdout(), d); err != nil {
						return err
					}
				}
			}
		}
		for _, p := range deleted {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "D  %s\n", p); err != nil {
				return err
			}
		}
		return nil
	}}
	cmd.Flags().BoolVar(&content, "content", true, "show unified diffs for modified files")
	return cmd
}