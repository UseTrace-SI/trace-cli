package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func agentHelpCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{Use: "agent-help", RunE: func(c *cobra.Command, _ []string) error {
		text := `Trace agent help

Commands:
  trace status              daemon, sessions, latest step, size
  trace log                 step timeline
  trace show <step>         prompt, reasoning, tools, diff
  trace diff <a> <b>        compare two keyframes
  trace why <file>:<line>    which step/agent touched a line
  trace search <query>      search turns and messages; --edited-path filters edits
  trace activity            local turn and session overview
  trace messages            conversation per turn
  trace rewind <step>       restore tree to a step (undoable)
  trace fork <step>         new worktree at a keyframe
  trace resume <step>       rewind + launch agent
  trace export --format agent-trace --out <file>
  trace gc / clean          prune old history
  trace doctor              self-diagnose
  trace tui                 interactive timeline

MCP tools (trace mcp, stdio, read-only):
  list_checkpoints, get_turn, get_context, why_line, search_history, get_messages
`
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]string{"help": text})
		}
		_, err := fmt.Fprint(c.OutOrStdout(), text)
		return err
	}}
}
