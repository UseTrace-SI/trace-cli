package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/restore"
	"github.com/spf13/cobra"
)

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func launchAgent(agent, root, context string) error {
	command, args, err := agentLaunchCommand(agent, context)
	if err != nil {
		return err
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = root
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("launching %s: %w", agent, err)
	}
	return nil
}

func agentLaunchCommand(agent, context string) (string, []string, error) {
	switch agent {
	case "claude-code":
		return "claude", []string{context}, nil
	case "codex":
		return "codex", []string{context}, nil
	case "agy":
		return "agy", []string{"--prompt-interactive", context}, nil
	case "cursor":
		return "agent", []string{context}, nil
	default:
		return "", nil, fmt.Errorf("unsupported agent %q", agent)
	}
}

func forkCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var agent, name string
	var noLaunch bool
	cmd := &cobra.Command{Use: "fork <step>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		step, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid step: %w", err)
		}
		if name != "" && !safeName.MatchString(name) {
			return fmt.Errorf("invalid fork name")
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
		var head string
		var origin string
		if err := e.DB.QueryRow(`SELECT COALESCE(k.git_head,''),COALESCE(t.session_id,'')
			FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id WHERE k.seq=?`, step).Scan(&head, &origin); err != nil {
			return fmt.Errorf("finding fork step: %w", err)
		}
		if head == "" {
			return fmt.Errorf("step %d has no git HEAD to fork", step)
		}
		target, err := e.ManifestAt(step)
		if err != nil {
			return err
		}
		base := filepath.Base(root) + "-fork-" + args[0]
		if name != "" {
			base += "-" + name
		}
		forkRoot := filepath.Join(filepath.Dir(root), base)
		if _, err := os.Stat(forkRoot); err == nil {
			return fmt.Errorf("fork path already exists: %s", forkRoot)
		} else if !os.IsNotExist(err) {
			return err
		}
		out, err := exec.Command("git", "-C", root, "worktree", "add", "--detach", forkRoot, head).CombinedOutput()
		if err != nil {
			return fmt.Errorf("creating worktree: %s: %w", out, err)
		}
		if _, err := restore.Apply(target, forkRoot, nil, e.Blobs); err != nil {
			return err
		}
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("finding Trace executable: %w", err)
		}
		init := exec.Command(self, "--repo", forkRoot, "init", "--agent", agent)
		if out, err := init.CombinedOutput(); err != nil {
			return fmt.Errorf("initializing fork: %s: %w", out, err)
		}
		if err := os.WriteFile(filepath.Join(forkRoot, ".trace", "forked-from"), []byte(fmt.Sprintf("%s:%d\n", origin, step)), 0600); err != nil {
			return fmt.Errorf("recording fork origin: %w", err)
		}
		context, err := e.Context(step)
		if err != nil {
			return err
		}
		if err := engine.WriteContext(forkRoot, context); err != nil {
			return err
		}
		if *jsonOutput {
			if err := json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"path": forkRoot, "step": step, "agent": agent}); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintln(c.OutOrStdout(), forkRoot); err != nil {
				return err
			}
		}
		if noLaunch {
			return nil
		}
		return launchAgent(agent, forkRoot, context)
	}}
	cmd.Flags().StringVar(&agent, "agent", "claude-code", "agent to launch")
	cmd.Flags().StringVar(&name, "name", "", "fork suffix")
	cmd.Flags().BoolVar(&noLaunch, "no-launch", false, "create worktree without launching agent")
	return cmd
}
