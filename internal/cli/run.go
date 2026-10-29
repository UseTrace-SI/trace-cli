package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/id"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
	"github.com/spf13/cobra"
)

// agentExit preserves the wrapped program's exit status without adding text to
// its output streams.
type agentExit struct{ code int }

func (e agentExit) Error() string { return fmt.Sprintf("agent exited with status %d", e.code) }

type boundedOutput struct {
	data      bytes.Buffer
	truncated bool
}

type synchronizedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func characterDevice(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err != nil || info.Mode()&os.ModeCharDevice != 0
}

func (w synchronizedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	const limit = 64 << 10
	remaining := limit - w.data.Len()
	if remaining < len(p) {
		w.truncated = true
	}
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = w.data.Write(p[:remaining])
	}
	return len(p), nil
}

func (w *boundedOutput) text() string {
	data := w.data.String()
	if w.truncated {
		// Avoid saving a partial last line, including a potentially truncated secret.
		if n := strings.LastIndexByte(data, '\n'); n >= 0 {
			data = data[:n+1]
		} else {
			return ""
		}
	}
	return textutil.TruncateBytes(data, 64<<10)
}

func runCommand(repoPath *string) *cobra.Command {
	var prompt string
	cmd := &cobra.Command{Use: "run <continue|vibe|crush|aider>", Short: "Run a single headless agent prompt with Trace", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		var binary string
		switch args[0] {
		case "continue":
			binary = "cn"
		case "vibe":
			binary = "vibe"
		case "crush":
			binary = "crush"
		case "aider":
			binary = "aider"
		default:
			return fmt.Errorf("unsupported headless agent %q", args[0])
		}
		if prompt == "" {
			return fmt.Errorf("--prompt is required")
		}
		path, err := exec.LookPath(binary)
		if err != nil {
			return fmt.Errorf("finding %s: %w", binary, err)
		}
		root, rootErr := rootFromFlag(*repoPath)
		if *repoPath != "" && rootErr != nil {
			return fmt.Errorf("finding repository: %w", rootErr)
		}
		session, idErr := id.New()
		started := false
		if rootErr == nil && idErr == nil {
			// Trace failures must not prevent the requested agent from running.
			if addExclude(root) == nil {
				if e, err := engine.Open(root); err == nil {
					if e.Baseline() == nil {
						ev := event.Event{Kind: event.TurnStart, Agent: args[0], AgentSessionID: session, Worktree: root, Prompt: prompt, At: time.Now().UnixMilli()}
						started = e.Process(ev) == nil
					}
					_ = e.Close()
				}
			}
		}
		agentArgs := []string{"-p", prompt}
		if args[0] == "crush" {
			agentArgs = []string{"run", "--", prompt}
		} else if args[0] == "aider" {
			// Aider normally commits edits and dirty files automatically. Keep
			// a Trace-launched run from creating git history on the user's behalf.
			agentArgs = []string{"--no-auto-commits", "--no-dirty-commits", "--message=" + prompt}
		}
		process := exec.Command(path, agentArgs...)
		process.Stdin, process.Stdout, process.Stderr = c.InOrStdin(), c.OutOrStdout(), c.ErrOrStderr()
		var output boundedOutput
		if started && args[0] == "vibe" && !characterDevice(c.OutOrStdout()) && !characterDevice(c.ErrOrStderr()) {
			var outputMu sync.Mutex
			process.Stdout = synchronizedWriter{mu: &outputMu, w: io.MultiWriter(c.OutOrStdout(), &output)}
			process.Stderr = synchronizedWriter{mu: &outputMu, w: c.ErrOrStderr()}
		}
		if *repoPath != "" && rootErr == nil {
			process.Dir = root
		}
		runErr := process.Run()
		if started {
			if e, err := engine.Open(root); err == nil {
				base := event.Event{Agent: args[0], AgentSessionID: session, Worktree: root, At: time.Now().UnixMilli()}
				if text := output.text(); text != "" {
					base.Kind, base.ContextType, base.Text = event.TurnContext, "output", text
					_ = e.Process(base)
					base.ContextType, base.Text = "", ""
				}
				base.Kind = event.ToolEdit
				_ = e.Process(base) // Unknown side effects require a fallback scan.
				base.Kind = event.TurnEnd
				_ = e.Process(base)
				base.Kind = event.SessionEnd
				_ = e.Process(base)
				_ = e.Close()
			}
		}
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			code := exit.ExitCode()
			if code < 0 {
				if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
					code = 128 + int(status.Signal())
				} else {
					code = 1
				}
			}
			return agentExit{code: code}
		}
		if runErr != nil {
			return fmt.Errorf("running %s: %w", binary, runErr)
		}
		return nil
	}}
	cmd.Flags().StringVar(&prompt, "prompt", "", "prompt for one headless agent run")
	return cmd
}
