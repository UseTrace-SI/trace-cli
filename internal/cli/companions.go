package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/companions"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	mcpserver "github.com/UseTrace-SI/trace-cli/internal/mcp"
	"github.com/spf13/cobra"
)

func contextCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var path string
	var limit int
	var window int
	var format string
	cmd := &cobra.Command{Use: "context [task words]", Short: "Combine history, memory and code context", RunE: func(c *cobra.Command, args []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		if window > 0 {
			limit = window
		}
		out, err := mcpserver.GetContext(c.Context(), e, mcpserver.ContextInput{Path: path, Query: strings.Join(args, " "), Limit: limit})
		if err != nil {
			return err
		}
		if format == "prompt" {
			fmt.Fprintln(c.OutOrStdout(), "## Trace context (injected)")
			for _, s := range out.Steps {
				fmt.Fprintf(c.OutOrStdout(), "- step %d [%s]: %s\n", s.Step, s.Agent, s.Prompt)
			}
			for _, m := range out.Brain.Memories {
				fmt.Fprintf(c.OutOrStdout(), "- memory [%s]: %s\n", m.Scope, m.Content)
			}
			for _, s := range out.Graph.Symbols {
				fmt.Fprintf(c.OutOrStdout(), "- symbol %s:%d %s %s\n", s.File, s.Line, s.Kind, s.Name)
			}
			for _, r := range out.Graph.Relations {
				fmt.Fprintf(c.OutOrStdout(), "- relation %s -> %s (%s:%d)\n", r.From, r.To, r.File, r.Line)
			}
			return nil
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(out)
		}
		fmt.Fprintf(c.OutOrStdout(), "history: %d turns; brain: %s; graph: %s\n", len(out.Steps), out.Brain.Status, out.Graph.Status)
		for _, s := range out.Steps {
			fmt.Fprintf(c.OutOrStdout(), "step %d [%s]: %s\n", s.Step, s.Agent, s.Prompt)
		}
		for _, m := range out.Brain.Memories {
			fmt.Fprintf(c.OutOrStdout(), "memory [%s]: %s\n", m.Scope, m.Content)
		}
		for _, s := range out.Graph.Symbols {
			fmt.Fprintf(c.OutOrStdout(), "symbol %s:%d %s %s\n", s.File, s.Line, s.Kind, s.Name)
		}
		for _, r := range out.Graph.Relations {
			fmt.Fprintf(c.OutOrStdout(), "relation %s -> %s (%s:%d)\n", r.From, r.To, r.File, r.Line)
		}
		return nil
	}}
	cmd.Flags().StringVar(&path, "path", "", "repository-relative file")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum results per component (1-50)")
	cmd.Flags().IntVar(&window, "window", 0, "sliding context window size (overrides --limit for steps)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text, json (use --json), or prompt")
	return cmd
}

func companionsCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "companions", Short: "Manage optional Brain and Graph integration"}
	cmd.AddCommand(&cobra.Command{Use: "refresh", Short: "Rebuild derived memory and code indexes", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		report := companions.Refresh(c.Context(), root)
		if err := companions.SaveStatus(root, report); err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(report)
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "brain: %s; graph: %s\n", report.Brain.Status, report.Graph.Status)
		return err
	}})
	cmd.AddCommand(&cobra.Command{Use: "status", Short: "Show the last refresh and automatic refresh setting", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		report := companions.Report{Protocol: 1, Brain: companions.State{Status: "not_refreshed"}, Graph: companions.State{Status: "not_refreshed"}}
		b, err := os.ReadFile(filepath.Join(root, ".trace", "companions", "status.json"))
		if err == nil {
			if err := json.Unmarshal(b, &report); err != nil {
				return fmt.Errorf("reading companion status: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		auto := companions.AutoEnabled(root)
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(struct {
				companions.Report
				Automatic bool `json:"automatic"`
			}{report, auto})
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "automatic: %t; brain: %s; graph: %s\n", auto, report.Brain.Status, report.Graph.Status)
		return err
	}})
	for _, mode := range []string{"enable-auto", "disable-auto"} {
		mode := mode
		cmd.AddCommand(&cobra.Command{Use: mode, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			root, err := initializedRoot(*repoPath)
			if err != nil {
				return err
			}
			file := filepath.Join(root, ".trace", "companions", "disabled")
			if mode == "disable-auto" {
				if err := os.Mkdir(filepath.Dir(file), 0700); err != nil && !os.IsExist(err) {
					return err
				}
				if err := os.WriteFile(file, []byte("1\n"), 0600); err != nil {
					return err
				}
			} else if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
				return err
			}
			if *jsonOutput {
				return json.NewEncoder(c.OutOrStdout()).Encode(map[string]bool{"automatic": companions.AutoEnabled(root)})
			}
			_, err = fmt.Fprintf(c.OutOrStdout(), "automatic companion refresh: %t\n", companions.AutoEnabled(root))
			return err
		}})
	}
	return cmd
}
