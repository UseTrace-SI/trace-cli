package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters"
	"github.com/UseTrace-SI/trace-cli/internal/userstate"
	"github.com/spf13/cobra"
)

func installedIntegrations() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, agent := range adapters.Globals() {
		if agent.Integration().Detected(home) {
			names = append(names, agent.Name())
		}
	}
	return names, nil
}

func knownIntegration(name string) bool {
	_, ok := adapters.ForGlobal(name)
	return ok
}

func agentsCommand(jsonOutput *bool) *cobra.Command {
	return &cobra.Command{Use: "agents", Short: "List supported global integrations, detection and recording state", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		configured, err := userstate.ConfiguredAgents()
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, name := range configured {
			known[name] = true
		}
		type row struct {
			Name       string `json:"name"`
			Kind       string `json:"interface"`
			Detected   bool   `json:"detected"`
			Configured bool   `json:"configured"`
			Paused     bool   `json:"paused"`
			Enable     string `json:"enable_command"`
		}
		globals := adapters.Globals()
		rows := make([]row, 0, len(globals))
		for _, agent := range globals {
			item, name := agent.Integration(), agent.Name()
			rows = append(rows, row{name, item.Kind, item.Detected(home), known[name], userstate.DisabledFor(name), "trace enable --agent " + name})
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"paused": userstate.Disabled(), "agents": rows, "enable_all_command": "trace enable --all", "scope": "supported user-level integrations; agent trust/restart/extension activation may still be required"})
		}
		for _, r := range rows {
			fmt.Fprintf(c.OutOrStdout(), "%-13s %-12s detected=%t configured=%t paused=%t\n", r.Name, r.Kind, r.Detected, r.Configured, r.Paused)
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), "Enable detected agents: trace enable --all\nEnable one agent: trace enable --agent NAME\nSome agents require hook trust, a restart or extension activation.")
		return err
	}}
}

func enableCommand(jsonOutput *bool) *cobra.Command {
	cmd := setupCommandWithDefaults(jsonOutput, nil)
	cmd.Use = "enable [agent ...]"
	cmd.Short = "Enable selected agents or automatically detect supported installed CLI/desktop agents"
	var all bool
	cmd.Flags().BoolVar(&all, "all", false, "enable all supported agents detected on this computer")
	_ = cmd.Flags().MarkHidden("remove")
	run := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		selected, _ := c.Flags().GetStringSlice("agent")
		if len(args) > 0 {
			if len(selected) > 0 {
				return fmt.Errorf("use agent names or --agent, not both")
			}
			selected = args
		}
		if all && len(selected) > 0 {
			return fmt.Errorf("choose --all or specific agents")
		}
		if remove, _ := c.Flags().GetBool("remove"); remove {
			return fmt.Errorf("use trace disable to pause recording, or trace setup --remove to uninstall hooks")
		}
		saved, err := userstate.ConfiguredAgents()
		if err != nil {
			return err
		}
		explicit := all || len(selected) > 0
		if !explicit {
			for _, name := range saved {
				if knownIntegration(name) && !userstate.AgentDisabled(name) {
					selected = append(selected, name)
				}
			}
		}
		if len(selected) == 0 {
			selected, err = installedIntegrations()
			if err != nil {
				return err
			}
		}
		if len(selected) == 0 {
			return fmt.Errorf("no supported installed agents detected; run trace agents --json or select one with --agent NAME")
		}
		for _, name := range selected {
			if !knownIntegration(name) {
				return fmt.Errorf("unknown global integration %q; run trace agents --json", name)
			}
		}
		value, ok := c.Flags().Lookup("agent").Value.(interface{ Replace([]string) error })
		if !ok {
			return fmt.Errorf("agent selection flag has an unsupported type")
		}
		if err := value.Replace(selected); err != nil {
			return err
		}
		// An explicit selection while globally paused resumes only those saved
		// integrations; a plain enable resumes the previously active selection.
		if explicit && userstate.Disabled() {
			for _, name := range saved {
				if err := userstate.SetAgentDisabled(name, true); err != nil {
					return err
				}
			}
		}
		originalOut := c.OutOrStdout()
		var output bytes.Buffer
		if *jsonOutput {
			c.SetOut(&output)
			defer c.SetOut(originalOut)
		}
		if err := run(c, nil); err != nil {
			return err
		}
		for _, name := range selected {
			if err := userstate.SetAgentDisabled(name, false); err != nil {
				return err
			}
		}
		if err := userstate.RememberAgents(selected); err != nil {
			return err
		}
		if *jsonOutput {
			var result map[string]any
			if err := json.Unmarshal(output.Bytes(), &result); err != nil {
				return err
			}
			result["activation_required"] = result["enabled"] == false
			delete(result, "enabled")
			result["configured"] = true
			result["paused"] = false
			result["selected_agents"] = selected
			return json.NewEncoder(originalOut).Encode(result)
		}
		_, err = fmt.Fprintln(originalOut, "Recording enabled. Approved projects sync automatically. Use trace disable to pause or trace agents to inspect integrations.")
		return err
	}
	return cmd
}

func disableCommand(jsonOutput *bool) *cobra.Command {
	var agents []string
	cmd := &cobra.Command{Use: "disable [agent ...]", Short: "Pause all recording/uploads or recording for selected agents", RunE: func(c *cobra.Command, args []string) error {
		if len(args) > 0 {
			if len(agents) > 0 {
				return fmt.Errorf("use agent names or --agent, not both")
			}
			agents = args
		}
		for _, name := range agents {
			if !knownIntegration(name) {
				return fmt.Errorf("unknown global integration %q; run trace agents --json", name)
			}
		}
		if len(agents) == 0 {
			if err := userstate.SetDisabled(true); err != nil {
				return err
			}
		} else {
			for _, name := range agents {
				if err := userstate.SetAgentDisabled(name, true); err != nil {
					return err
				}
			}
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"paused": true, "all": len(agents) == 0, "agents": agents})
		}
		if len(agents) > 0 {
			_, err := fmt.Fprintf(c.OutOrStdout(), "Recording paused for %s. Other enabled agents and existing history uploads are unchanged.\n", strings.Join(agents, ", "))
			return err
		}
		_, err := fmt.Fprintln(c.OutOrStdout(), "Trace paused across projects. History and login are preserved. Run trace enable to resume.")
		return err
	}}
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "agents to pause; omit to pause recording and automatic uploads for this user")
	return cmd
}
