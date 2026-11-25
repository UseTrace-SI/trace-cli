package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters"
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
	return runAgent(agent, root, command, args)
}

// runAgent runs an agent's launch command interactively in root.
func runAgent(agent, root, command string, args []string) error {
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
	if a, ok := adapters.Lookup(agent); ok {
		if launcher, ok := a.(adapters.Launcher); ok {
			name, args := launcher.LaunchCommand(context)
			return name, args, nil
		}
	}
	return "", nil, fmt.Errorf("agent %q cannot be launched; agents with a launch capability: %s", agent, strings.Join(adapters.LauncherNames(), ", "))
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
		forkRoot, keep, err := createFork(e, root, args[0], step, agent, name)
		if err != nil {
			return err
		}
		keepWorktree := false
		defer func() { keep(keepWorktree) }()
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
		keepWorktree = true
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

// createFork adds a detached worktree beside root at step, restores the
// step's files into it and initializes Trace there for agent. Call keep with
// true to keep the worktree; with false it is removed.
func createFork(e *engine.Engine, root, label string, step int64, agent, name string) (string, func(bool), error) {
	forkRoot, remove, err := prepareFork(e, root, label, step, agent, name)
	if err != nil {
		if remove != nil {
			remove()
		}
		return "", nil, err
	}
	return forkRoot, func(keep bool) {
		if !keep {
			remove()
		}
	}, nil
}

func prepareFork(e *engine.Engine, root, label string, step int64, agent, name string) (string, func(), error) {
	var head string
	var origin string
	if err := e.DB.QueryRow(`SELECT COALESCE(k.git_head,''),COALESCE(t.session_id,'')
			FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id WHERE k.seq=?`, step).Scan(&head, &origin); err != nil {
		return "", nil, fmt.Errorf("finding fork step: %w", err)
	}
	if head == "" {
		return "", nil, fmt.Errorf("step %d has no git HEAD to fork", step)
	}
	target, err := e.ManifestAt(step)
	if err != nil {
		return "", nil, err
	}
	base := filepath.Base(root) + "-fork-" + label
	if name != "" {
		base += "-" + name
	}
	forkRoot := filepath.Join(filepath.Dir(root), base)
	if _, err := os.Stat(forkRoot); err == nil {
		return "", nil, fmt.Errorf("fork path already exists: %s", forkRoot)
	} else if !os.IsNotExist(err) {
		return "", nil, err
	}
	out, err := exec.Command("git", "-C", root, "worktree", "add", "--detach", forkRoot, head).CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("creating worktree: %s: %w", out, err)
	}
	// Any failure after this point would otherwise leave an orphaned
	// worktree registered in the source repository. Keep it only once the
	// fork has been fully prepared.
	remove := func() {
		if out, err := exec.Command("git", "-C", root, "worktree", "remove", "--force", forkRoot).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "trace: removing failed fork worktree: %s: %v\n", strings.TrimSpace(string(out)), err)
		}
	}
	if _, err := restore.Apply(target, forkRoot, nil, e.Blobs); err != nil {
		return "", remove, err
	}
	self, err := os.Executable()
	if err != nil {
		return "", remove, fmt.Errorf("finding Trace executable: %w", err)
	}
	init := exec.Command(self, "--repo", forkRoot, "init", "--agent", agent)
	if out, err := init.CombinedOutput(); err != nil {
		return "", remove, fmt.Errorf("initializing fork: %s: %w", out, err)
	}
	if err := os.WriteFile(filepath.Join(forkRoot, ".trace", "forked-from"), []byte(fmt.Sprintf("%s:%d\n", origin, step)), 0600); err != nil {
		return "", remove, fmt.Errorf("recording fork origin: %w", err)
	}
	return forkRoot, remove, nil
}
