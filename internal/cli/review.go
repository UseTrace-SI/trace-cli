package cli

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

func reviewCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var agent string
	var noLaunch bool
	cmd := &cobra.Command{Use: "review", Short: "Run a multi-agent review against current changes", RunE: func(c *cobra.Command, _ []string) error {
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		out, _ := exec.Command("git", "-C", root, "diff", "--stat").CombinedOutput()
		diff, _ := exec.Command("git", "-C", root, "diff").CombinedOutput()
		prompt := "Review the following changes for correctness, security, and style:\n\n" + string(out) + "\n" + string(diff)
		if noLaunch {
			fmt.Fprint(c.OutOrStdout(), prompt)
			return nil
		}
		return launchAgent(agent, root, prompt)
	}}
	cmd.Flags().StringVar(&agent, "agent", "claude-code", "agent to launch")
	cmd.Flags().BoolVar(&noLaunch, "no-launch", false, "print review prompt without launching agent")
	return cmd
}