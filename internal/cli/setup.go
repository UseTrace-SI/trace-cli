package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters"
	"github.com/UseTrace-SI/trace-cli/internal/userstate"
	"github.com/spf13/cobra"
)

func setupAgent(home, name string, remove bool) error {
	agent, ok := adapters.ForGlobal(name)
	if !ok {
		return fmt.Errorf("global setup is not available for agent %q", name)
	}
	return agent.SetupGlobal(home, remove)
}

func setupCommand(jsonOutput *bool) *cobra.Command {
	return setupCommandWithDefaults(jsonOutput, nil)
}

func setupCommandWithDefaults(jsonOutput *bool, defaults []string) *cobra.Command {
	var agents []string
	var remove bool
	cmd := &cobra.Command{Use: "setup", Short: "Enable automatic recording across repositories", RunE: func(c *cobra.Command, _ []string) error {
		if len(agents) == 0 {
			agents = defaults
			if len(agents) == 0 {
				agents = []string{"claude-code", "codex"}
			}
		}
		selected := make([]adapters.GlobalInstaller, 0, len(agents))
		for _, name := range agents {
			agent, ok := adapters.ForGlobal(name)
			if !ok {
				return fmt.Errorf("global setup is not available for agent %q", name)
			}
			selected = append(selected, agent)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("finding home directory: %w", err)
		}
		for _, agent := range selected {
			if !remove && agent.SetupNeedsStateDir() {
				if err := os.MkdirAll(filepath.Join(home, ".trace"), 0700); err != nil {
					return fmt.Errorf("creating setup state directory: %w", err)
				}
			}
			if err := agent.SetupGlobal(home, remove); err != nil {
				return fmt.Errorf("setting up %s: %w", agent.Name(), err)
			}
		}
		if !remove {
			if err := userstate.SetDisabled(false); err != nil {
				return fmt.Errorf("enabling Trace: %w", err)
			}
		}
		if *jsonOutput {
			result := map[string]any{"agents": agents, "enabled": !remove}
			var additional []string
			for _, agent := range selected {
				notice, extra := agent.SetupJSONNotice(home, remove)
				if notice == "" {
					continue
				}
				result["enabled"] = false
				additional = append(additional, notice)
				for key, value := range extra {
					result[key] = value
				}
			}
			if len(additional) > 0 {
				result["additional_setup"] = strings.Join(additional, " ")
			}
			return json.NewEncoder(c.OutOrStdout()).Encode(result)
		}
		if remove {
			message := "Global Trace hooks removed."
			for _, agent := range selected {
				if notice := agent.SetupRemovedNotice(); notice != "" {
					message += " " + notice
				}
			}
			_, err = fmt.Fprintln(c.OutOrStdout(), message)
			return err
		}
		message := "Automatic recording enabled."
		for _, agent := range selected {
			if _, ok := agent.SetupHeadline(); ok {
				message = "Selected integrations installed."
				break
			}
		}
		if len(selected) == 1 {
			if solo, _ := selected[0].SetupHeadline(); solo != "" {
				message = solo
			}
		}
		for _, agent := range selected {
			if notice := agent.SetupNotice(home); notice != "" {
				message += " " + notice
			}
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), message)
		return err
	}}
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "agents to enable ("+strings.Join(adapters.GlobalNames(), ", ")+")")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove selected global hooks")
	return cmd
}
