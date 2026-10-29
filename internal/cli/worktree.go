package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/gitx"
	"github.com/spf13/cobra"
)

func worktreeCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "worktree", Hidden: true}
	gc := &cobra.Command{Use: "gc", RunE: func(c *cobra.Command, _ []string) error {
		dryRun, _ := c.Flags().GetBool("dry-run")
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		paths, err := gitx.WorktreeList(root)
		if err != nil {
			return err
		}
		var candidates []string
		for _, p := range paths {
			if p == root {
				continue
			}
			if _, err := os.Stat(filepath.Join(p, ".trace", "forked-from")); err != nil {
				continue
			}
			if !gitx.IsClean(p) {
				continue
			}
			candidates = append(candidates, p)
		}
		if dryRun {
			if *jsonOutput {
				return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"candidates": candidates, "dry_run": true})
			}
			if len(candidates) == 0 {
				_, err = fmt.Fprintln(c.OutOrStdout(), "No Trace-created worktrees to clean.")
				return err
			}
			for _, p := range candidates {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "would remove: %s\n", p); err != nil {
					return err
				}
			}
			return nil
		}
		var removed []string
		for _, p := range candidates {
			if err := gitx.WorktreeRemove(root, p, false); err != nil {
				if _, e := fmt.Fprintf(c.ErrOrStderr(), "failed to remove %s: %v\n", p, err); e != nil {
					return e
				}
				continue
			}
			removed = append(removed, p)
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"removed": removed})
		}
		if len(removed) == 0 {
			_, err = fmt.Fprintln(c.OutOrStdout(), "No Trace-created worktrees removed.")
			return err
		}
		for _, p := range removed {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "removed: %s\n", p); err != nil {
				return err
			}
		}
		return nil
	}}
	gc.Flags().Bool("dry-run", false, "preview without removing")
	cmd.AddCommand(gc)
	return cmd
}