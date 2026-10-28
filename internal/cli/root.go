package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func Execute(version, commit, date string) int {
	var jsonOutput bool
	var repoPath string
	root := &cobra.Command{Use: "trace", Short: "Local coding-agent history", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "machine-readable output")
	root.PersistentFlags().StringVar(&repoPath, "repo", "", "repository root")
	root.AddCommand(versionCommand(version, commit, date, &jsonOutput))
	root.AddCommand(daemonCommand(&repoPath, &jsonOutput))
	root.AddCommand(hookCommand(&repoPath))
	root.AddCommand(initCommand(&repoPath, &jsonOutput))
	root.AddCommand(uninstallCommand(&repoPath, &jsonOutput))
	root.AddCommand(logCommand(&repoPath, &jsonOutput))
	root.AddCommand(showCommand(&repoPath, &jsonOutput))
	root.AddCommand(rewindCommand(&repoPath, &jsonOutput))
	root.AddCommand(statusCommand(&repoPath, &jsonOutput))
	root.AddCommand(whyCommand(&repoPath, &jsonOutput))
	root.AddCommand(forkCommand(&repoPath, &jsonOutput))
	root.AddCommand(resumeCommand(&repoPath, &jsonOutput))
	root.AddCommand(mcpCommand(&repoPath))
	root.AddCommand(gcCommand(&repoPath, &jsonOutput))
	root.AddCommand(updateCommand(&repoPath, &jsonOutput, version))
	root.AddCommand(exportCommand(&repoPath, &jsonOutput))
	root.AddCommand(loginCommand(&repoPath, &jsonOutput))
	root.AddCommand(syncCommand(&repoPath, &jsonOutput))
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
