package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/userstate"
	"github.com/spf13/cobra"
)

type integration struct {
	Name     string
	Kind     string
	Commands []string
	Profiles []string
	Apps     []string
}

var globalIntegrations = []integration{
	{"claude-code", "CLI", []string{"claude"}, []string{".claude"}, nil},
	{"codex", "CLI/Desktop", []string{"codex"}, []string{".codex"}, []string{"Codex.app"}},
	{"cursor", "CLI/Desktop", []string{"cursor", "agent"}, []string{".cursor"}, []string{"Cursor.app"}},
	{"windsurf", "Desktop", []string{"windsurf"}, []string{".codeium/windsurf"}, []string{"Windsurf.app", "Devin.app"}},
	{"copilot", "CLI", []string{"copilot"}, []string{".copilot"}, nil},
	{"gemini", "CLI", []string{"gemini"}, []string{".gemini"}, nil},
	{"qwen", "CLI", []string{"qwen"}, []string{".qwen"}, nil},
	{"factory", "CLI", []string{"droid"}, []string{".factory"}, nil},
	{"amp", "CLI", []string{"amp"}, []string{".config/amp"}, nil},
	{"pi", "CLI", []string{"pi"}, []string{".pi"}, nil},
	{"omp", "CLI", []string{"omp"}, []string{".omp"}, nil},
	{"kilo", "CLI", []string{"kilo"}, []string{".config/kilo"}, nil},
	{"opencode", "CLI/Desktop", []string{"opencode"}, []string{".config/opencode"}, []string{"OpenCode.app"}},
	{"openclaude", "CLI", []string{"openclaude"}, []string{".openclaude"}, nil},
	{"kiro", "CLI", []string{"kiro-cli", "kiro"}, []string{".kiro"}, nil},
	{"kimi", "CLI", []string{"kimi"}, []string{".kimi-code"}, nil},
	{"auggie", "CLI", []string{"auggie"}, []string{".augment"}, nil},
	{"openhands", "CLI", []string{"openhands"}, []string{".openhands"}, nil},
	{"cline", "CLI", []string{"cline"}, []string{".cline"}, nil},
	{"poolside", "CLI", []string{"pool"}, []string{".config/poolside"}, nil},
	{"hermes", "CLI", []string{"hermes"}, []string{".hermes"}, nil},
	{"zoo", "Desktop", nil, []string{".vscode/extensions/zoocodeorganization.zoo-code-*", ".vscode-insiders/extensions/zoocodeorganization.zoo-code-*"}, nil},
}

func integrationDetected(item integration, home string) bool {
	for _, name := range item.Commands {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	for _, profile := range item.Profiles {
		matches, _ := filepath.Glob(filepath.Join(home, profile))
		if len(matches) > 0 {
			return true
		}
	}
	for _, app := range item.Apps {
		for _, directory := range []string{"/Applications", filepath.Join(home, "Applications")} {
			if _, err := os.Stat(filepath.Join(directory, app)); err == nil {
				return true
			}
		}
	}
	return false
}

func installedIntegrations() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, item := range globalIntegrations {
		if integrationDetected(item, home) {
			names = append(names, item.Name)
		}
	}
	return names, nil
}

func knownIntegration(name string) bool {
	for _, item := range globalIntegrations {
		if item.Name == name {
			return true
		}
	}
	return false
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
		rows := make([]row, 0, len(globalIntegrations))
		for _, item := range globalIntegrations {
			rows = append(rows, row{item.Name, item.Kind, integrationDetected(item, home), known[item.Name], userstate.DisabledFor(item.Name), "trace enable --agent " + item.Name})
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
		if err := c.Flags().Set("agent", strings.Join(selected, ",")); err != nil {
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
