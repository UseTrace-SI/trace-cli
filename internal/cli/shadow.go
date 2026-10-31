package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/gitx"
	"github.com/spf13/cobra"
)

func shadowCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "shadow", Short: "Manage shadow branches for in-session rewind"}
	cmd.AddCommand(&cobra.Command{Use: "start <name>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		if err := gitx.CreateShadow(root, args[0]); err != nil {
			return err
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "created shadow branch trace/shadow/%s\n", args[0])
		return err
	}})
	cmd.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		names, err := gitx.ListShadows(root)
		if err != nil {
			return err
		}
		for _, n := range names {
			fmt.Fprintln(c.OutOrStdout(), n)
		}
		return nil
	}})
	cmd.AddCommand(&cobra.Command{Use: "delete <name>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		if err := gitx.DeleteShadow(root, args[0]); err != nil {
			return err
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "deleted shadow branch trace/shadow/%s\n", args[0])
		return err
	}})
	cmd.AddCommand(&cobra.Command{Use: "restore <name> <dest>", Args: cobra.ExactArgs(2), RunE: func(c *cobra.Command, args []string) error {
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		dest := args[1]
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(root, dest)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		if err := gitx.RestoreShadow(root, args[0], dest); err != nil {
			return err
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "restored shadow to %s\n", dest)
		return err
	}})
	return cmd
}