package cli

import (
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/gitx"
	"github.com/spf13/cobra"
)

func gitNativeCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "git-native", Short: "Manage opt-in git-native checkpoint storage"}
	cmd.AddCommand(&cobra.Command{Use: "enable", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if err := gitx.EnableGitNative(root); err != nil {
			return err
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), "git-native checkpoint storage enabled")
		return err
	}})
	cmd.AddCommand(&cobra.Command{Use: "disable", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if err := gitx.DisableGitNative(root); err != nil {
			return err
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), "git-native checkpoint storage disabled")
		return err
	}})
	cmd.AddCommand(&cobra.Command{Use: "checkpoint", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if err := gitx.Checkpoint(root); err != nil {
			return err
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), "checkpoint saved to "+gitx.CheckpointBranch)
		return err
	}})
	return cmd
}