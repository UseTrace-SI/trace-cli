package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	mcpserver "github.com/UseTrace-SI/trace-cli/internal/mcp"
	"github.com/spf13/cobra"
)

func handoffCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var out string
	cmd := &cobra.Command{Use: "handoff", Short: "Export a portable handoff bundle with context for another machine/agent", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if out == "" {
			out = filepath.Join(root, "trace-handoff.tar.gz")
		}
		if err := writeBundle(root, out); err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		ctx, err := mcpserver.GetContext(c.Context(), e, mcpserver.ContextInput{Query: "recent work", Limit: 10})
		if err != nil {
			return err
		}
		ctxFile := filepath.Join(root, "trace-handoff-context.md")
		f, err := os.Create(ctxFile)
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Fprintln(f, "# Trace handoff context")
		for _, s := range ctx.Steps {
			fmt.Fprintf(f, "- step %d [%s]: %s\n", s.Step, s.Agent, s.Prompt)
		}
		for _, m := range ctx.Brain.Memories {
			fmt.Fprintf(f, "- memory [%s]: %s\n", m.Scope, m.Content)
		}
		for _, s := range ctx.Graph.Symbols {
			fmt.Fprintf(f, "- symbol %s:%d %s %s\n", s.File, s.Line, s.Kind, s.Name)
		}
		if *jsonOutput {
			return nil
		}
		fmt.Fprintf(c.OutOrStdout(), "handoff bundle: %s\ncontext: %s\n", out, ctxFile)
		return nil
	}}
	cmd.Flags().StringVar(&out, "out", "", "output bundle path")
	return cmd
}