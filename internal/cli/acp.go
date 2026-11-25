package cli

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/UseTrace-SI/trace-cli/internal/acp"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/UseTrace-SI/trace-cli/internal/userstate"
	"github.com/spf13/cobra"
)

func acpCommand(repoPath *string) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "acp [--name NAME] -- <agent command> [args...]",
		Short: "Run an Agent Client Protocol agent and record its turns",
		Long: `Run an Agent Client Protocol (ACP) agent behind a transparent stdio proxy.

Configure your editor (Zed, JetBrains, ...) to launch "trace acp -- <agent>"
instead of the agent. Every message is forwarded unchanged in both directions;
sessions, prompts, responses, tool calls and file edits in repositories where
Trace is initialized are recorded as agent "acp:<name>".`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if name == "" {
				name = acpAgentName(args[0])
			}
			logger := newACPLogger()
			defer logger.close()
			sink := &acpSink{repoFlag: *repoPath, logf: logger.logf, roots: map[string]string{}}
			signals := make(chan os.Signal, 4)
			signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
			// A closed editor pipe must surface as a write error, not kill
			// Trace before it can stop the agent.
			signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
			defer signal.Stop(signals)
			err := acp.Run(acp.Config{
				Args: args, Name: "acp:" + name,
				Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
				Sink: sink.emit, Logf: logger.logf, Signals: signals,
			})
			if exit, ok := err.(acp.ExitError); ok {
				return agentExit{code: exit.Code}
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "trace acp:", err)
				return agentExit{code: 127}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "agent name recorded as acp:<name> (default: the command's base name)")
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// acpAgentName derives a stable agent name from the command, for example
// "/usr/bin/claude-agent-acp" -> "claude-agent-acp".
func acpAgentName(command string) string {
	base := strings.ToLower(filepath.Base(command))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_' || r == '.' || r == ' ':
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "agent"
	}
	return b.String()
}

// acpSink sends events through the hook path: daemon socket or spool.
// Sessions outside a Git repository with an initialized .trace are ignored.
type acpSink struct {
	repoFlag string
	logf     func(string, ...any)
	mu       sync.Mutex
	roots    map[string]string // session cwd -> repository root ("" = not recorded)
	started  map[string]bool
}

func (s *acpSink) emit(ev event.Event) {
	if userstate.DisabledFor(ev.Agent) {
		return
	}
	root := s.root(ev.Worktree)
	if root == "" {
		return
	}
	ev.Worktree = root
	s.mu.Lock()
	if s.started == nil {
		s.started = map[string]bool{}
	}
	start := !s.started[root]
	s.started[root] = true
	s.mu.Unlock()
	if start {
		daemon.StartAsync(root)
	}
	if err := daemon.Send(filepath.Join(root, ".trace"), ev); err != nil {
		s.logf("recording %s event: %v", ev.Kind, err)
	}
}

func (s *acpSink) root(cwd string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if root, ok := s.roots[cwd]; ok {
		return root
	}
	dir := cwd
	if s.repoFlag != "" {
		dir = s.repoFlag
	}
	root, err := repo.FindRoot(dir)
	if err == nil {
		if info, statErr := os.Lstat(filepath.Join(root, ".trace")); statErr != nil || !info.IsDir() {
			s.logf("trace is not initialized in the session's repository; not recording it")
			root = ""
		}
	} else {
		s.logf("session directory is not in a Git repository; not recording it")
		root = ""
	}
	s.roots[cwd] = root
	return root
}

// acpLogger appends observation problems to <TRACE_HOME>/acp.log. It never
// writes to stdout, which carries the protocol, and never logs message bodies.
type acpLogger struct {
	once   sync.Once
	file   *os.File
	logger *slog.Logger
	mu     sync.Mutex
}

func newACPLogger() *acpLogger { return &acpLogger{} }

func (l *acpLogger) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.once.Do(func() {
		path, err := userstate.Path("acp.log")
		if err != nil {
			return
		}
		if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
			return
		}
		if info, err := os.Stat(path); err == nil && info.Size() > 1<<20 {
			_ = os.Truncate(path, 0)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		l.file = f
		l.logger = slog.New(slog.NewTextHandler(f, nil)).With("pid", os.Getpid())
	})
	if l.logger == nil {
		return
	}
	l.logger.Warn(fmt.Sprintf(format, args...))
}

func (l *acpLogger) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
	}
}
