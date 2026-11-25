package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

// contextPlaceholder stands for the handoff text in printed commands.
const contextPlaceholder = "<handoff context>"

type resumeOptions struct {
	agent    string
	noLaunch bool
	noRewind bool
	fork     bool
	name     string
	maxBytes int
}

func resumeCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var o resumeOptions
	cmd := &cobra.Command{
		Use:   "resume <step|session>",
		Short: "Continue a recorded session, in the same or another agent",
		Long: `Resume rewinds the working tree to a step (or a session's latest step) and
starts an agent with a redacted, size-capped handoff: the original goal,
the turns, the files changed with line counts, open items from the last
response and how to see more. The handoff is also written to
.trace/context.md. --fork restores into a new worktree instead of
rewinding, --no-rewind keeps the tree as it is, and --no-launch prints
the plan and the handoff without starting the agent.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runResume(c, *repoPath, *jsonOutput, args[0], o)
		},
	}
	cmd.Flags().StringVar(&o.agent, "agent", "", "agent to launch (default: the session's agent when it can be launched, else claude-code)")
	cmd.Flags().BoolVar(&o.noLaunch, "no-launch", false, "print the plan and handoff context without launching")
	cmd.Flags().BoolVar(&o.noRewind, "no-rewind", false, "keep the working tree as it is")
	cmd.Flags().BoolVar(&o.fork, "fork", false, "restore the step into a new worktree instead of rewinding")
	cmd.Flags().StringVar(&o.name, "name", "", "fork suffix (with --fork)")
	cmd.Flags().IntVar(&o.maxBytes, "max-context-bytes", engine.DefaultHandoffBytes, fmt.Sprintf("cap on the handoff context size (at least %d)", engine.MinHandoffBytes))
	return cmd
}

func runResume(c *cobra.Command, repoPath string, jsonOutput bool, target string, o resumeOptions) error {
	if o.fork && o.noRewind {
		return fmt.Errorf("--fork and --no-rewind cannot be combined")
	}
	if o.name != "" && (!o.fork || !safeName.MatchString(o.name)) {
		return fmt.Errorf("invalid fork name (--name needs --fork and letters, digits, - or _)")
	}
	if o.maxBytes < engine.MinHandoffBytes {
		return fmt.Errorf("--max-context-bytes must be at least %d", engine.MinHandoffBytes)
	}
	root, err := initializedRoot(repoPath)
	if err != nil {
		return err
	}
	e, err := engine.Open(root)
	if err != nil {
		return err
	}
	defer e.Close()
	step, err := e.ResolveResumeTarget(target)
	if err != nil {
		return err
	}
	handoff, err := e.Handoff(step, o.maxBytes)
	if err != nil {
		return err
	}
	agent := o.agent
	if agent == "" {
		agent = "claude-code"
		if _, _, err := agentLaunchCommand(handoff.Agent, ""); err == nil {
			agent = handoff.Agent
		}
	}
	// Check the agent before touching the working tree.
	program, argv, err := agentLaunchCommand(agent, handoff.Text)
	if err != nil {
		return err
	}
	_, shown, _ := agentLaunchCommand(agent, contextPlaceholder)

	mode := "rewind"
	workRoot := root
	var safety int64
	result := map[string]any{"step": step, "agent": agent}
	switch {
	case o.fork:
		mode = "fork"
		forkRoot, keep, err := createFork(e, root, fmt.Sprint(step), step, agent, o.name)
		if err != nil {
			return err
		}
		if err := engine.WriteContext(forkRoot, handoff.Text); err != nil {
			keep(false)
			return err
		}
		keep(true)
		workRoot = forkRoot
	case o.noRewind:
		mode = "in-place"
		if err := engine.WriteContext(root, handoff.Text); err != nil {
			return err
		}
	default:
		safety, _, err = e.Rewind(step, "", false)
		if err == nil {
			err = engine.WriteContext(root, handoff.Text)
		}
		if err != nil {
			result["safety_step"] = safety
			return rewindFailure(c, jsonOutput, result, safety, err)
		}
		result["safety_step"] = safety
	}
	contextFile := filepath.Join(workRoot, ".trace", "context.md")
	result["mode"] = mode
	result["path"] = workRoot
	result["session_id"] = handoff.SessionID
	result["source_agent"] = handoff.Agent
	result["context_file"] = contextFile
	result["context_bytes"] = len(handoff.Text)
	result["truncated"] = handoff.Truncated
	result["files"] = handoff.Files
	result["todos"] = handoff.Todos
	result["command"] = append([]string{program}, shown...)
	result["launched"] = !o.noLaunch
	if jsonOutput {
		if o.noLaunch {
			result["context"] = handoff.Text
		}
		if err := json.NewEncoder(c.OutOrStdout()).Encode(result); err != nil {
			return err
		}
	} else if err := printResumePlan(c.OutOrStdout(), mode, step, safety, workRoot, agent, program, shown, contextFile, handoff, o.noLaunch); err != nil {
		return err
	}
	if o.noLaunch {
		return nil
	}
	return runAgent(agent, workRoot, program, argv)
}

func printResumePlan(w io.Writer, mode string, step, safety int64, workRoot, agent, program string, shown []string, contextFile string, h engine.Handoff, noLaunch bool) error {
	var s strings.Builder
	switch mode {
	case "fork":
		fmt.Fprintf(&s, "Forked step %d into %s\n", step, workRoot)
	case "in-place":
		fmt.Fprintf(&s, "Resuming from step %d without rewinding\n", step)
	default:
		fmt.Fprintf(&s, "Resumed at step %d; undo with trace rewind %d\n", step, safety)
	}
	verb := "Launching"
	if noLaunch {
		verb = "Would launch"
	}
	fmt.Fprintf(&s, "%s %s: %s %s\n", verb, agent, program, strings.Join(shown, " "))
	fmt.Fprintf(&s, "Handoff context (%d bytes%s): %s\n", len(h.Text), map[bool]string{true: ", truncated", false: ""}[h.Truncated], contextFile)
	if noLaunch {
		fmt.Fprintf(&s, "\n%s", h.Text)
	}
	_, err := io.WriteString(w, s.String())
	return err
}
