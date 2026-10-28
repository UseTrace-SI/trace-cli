package cli

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func resumeCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var agent string
	var noLaunch bool
	cmd := &cobra.Command{Use: "resume <step>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		step, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid step: %w", err)
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
		context, err := e.Context(step)
		if err != nil {
			return err
		}
		safety, _, err := e.Rewind(step, "", false)
		if err != nil {
			return err
		}
		if err := engine.WriteContext(root, context); err != nil {
			return err
		}
		if *jsonOutput {
			if err := json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"step": step, "safety_step": safety, "agent": agent}); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "Resumed at step %d; undo with trace rewind %d\n", step, safety); err != nil {
				return err
			}
		}
		if noLaunch {
			return nil
		}
		return launchAgent(agent, root, context)
	}}
	cmd.Flags().StringVar(&agent, "agent", "claude-code", "agent to launch")
	cmd.Flags().BoolVar(&noLaunch, "no-launch", false, "rewind and write context without launching")
	return cmd
}
