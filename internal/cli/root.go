package cli

import (
	"errors"
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
	root.AddCommand(setupCommand(&jsonOutput))
	root.AddCommand(runCommand(&repoPath))
	root.AddCommand(uninstallCommand(&repoPath, &jsonOutput))
	root.AddCommand(logCommand(&repoPath, &jsonOutput))
	root.AddCommand(showCommand(&repoPath, &jsonOutput))
	root.AddCommand(rewindCommand(&repoPath, &jsonOutput))
	root.AddCommand(statusCommand(&repoPath, &jsonOutput))
	root.AddCommand(tuiCommand(&repoPath, &jsonOutput))
	root.AddCommand(doctorCommand(&repoPath, &jsonOutput))
	root.AddCommand(whyCommand(&repoPath, &jsonOutput))
	root.AddCommand(diffCommand(&repoPath, &jsonOutput))
	root.AddCommand(messagesCommand(&repoPath, &jsonOutput))
	root.AddCommand(searchCommand(&repoPath, &jsonOutput))
	root.AddCommand(activityCommand(&repoPath, &jsonOutput))
	root.AddCommand(worktreeCommand(&repoPath, &jsonOutput))
	root.AddCommand(forkCommand(&repoPath, &jsonOutput))
	root.AddCommand(resumeCommand(&repoPath, &jsonOutput))
	root.AddCommand(mcpCommand(&repoPath))
	root.AddCommand(gcCommand(&repoPath, &jsonOutput))
	root.AddCommand(updateCommand(&repoPath, &jsonOutput, version))
	root.AddCommand(exportCommand(&repoPath, &jsonOutput))
	root.AddCommand(loginCommand(&repoPath, &jsonOutput))
	root.AddCommand(syncCommand(&repoPath, &jsonOutput))
	root.AddCommand(agentCommand(&repoPath, &jsonOutput))
	root.AddCommand(dispatchCommand(&repoPath, &jsonOutput))
	root.AddCommand(cleanCommand(&repoPath, &jsonOutput))
	root.AddCommand(agentHelpCommand(&repoPath, &jsonOutput))
	loadPlugins(root)
	if err := root.Execute(); err != nil {
		var agentErr agentExit
		if errors.As(err, &agentErr) {
			return agentErr.code
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
