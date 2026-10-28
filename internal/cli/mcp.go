package cli

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	mcpserver "github.com/UseTrace-SI/trace-cli/internal/mcp"
)

func mcpCommand(repoPath *string) *cobra.Command {
	return &cobra.Command{Use: "mcp", Short: "Serve read-only Trace tools over MCP stdio", RunE: func(_ *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		return mcpserver.New(e).Run(context.Background(), &mcp.StdioTransport{})
	}}
}
