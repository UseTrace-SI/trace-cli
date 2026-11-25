package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters"
	"github.com/UseTrace-SI/trace-cli/internal/companions"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/gitx"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/userstate"
	"github.com/UseTrace-SI/trace-cli/internal/watch"
	"github.com/spf13/cobra"
)

const maxDaemonLogBytes = 10 << 20

type rotatingLog struct {
	mu    sync.Mutex
	path  string
	file  *os.File
	size  int64
	limit int64
}

func openDaemonLog(path string) (*rotatingLog, error) {
	return openRotatingLog(path, maxDaemonLogBytes)
}

func openRotatingLog(path string, limit int64) (*rotatingLog, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > limit {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &rotatingLog{path: path, file: f, size: info.Size(), limit: limit}, nil
}

func (l *rotatingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return 0, os.ErrClosed
	}
	if l.size+int64(len(p)) > l.limit {
		_ = l.file.Close()
		_ = os.Rename(l.path, l.path+".1")
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return 0, err
		}
		l.file = f
		l.size = 0
	}
	n, err := l.file.Write(p)
	l.size += int64(n)
	return n, err
}

func (l *rotatingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func daemonCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "daemon", Hidden: true, RunE: func(_ *cobra.Command, _ []string) error {
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		// Hooks start the daemon in any repository an agent opens. Refuse a
		// .trace that the repository controls before writing any state.
		if err := daemon.CheckTraceDir(root); err != nil {
			return err
		}
		dir := filepath.Join(root, ".trace")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating trace directory: %w", err)
		}
		lock, acquired, err := daemon.TryAcquireDaemonLock(dir)
		if err != nil {
			return err
		}
		if !acquired {
			return nil
		}
		defer daemon.ReleaseDaemonLock(lock)
		logfile, err := openDaemonLog(filepath.Join(dir, "daemon.log"))
		if err != nil {
			return fmt.Errorf("opening daemon log: %w", err)
		}
		defer logfile.Close()
		logger := slog.New(slog.NewTextHandler(logfile, nil))
		redact.EnsureMarkerKey()
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		// Lazily take a baseline so a daemon started in an un-initialized repo
		// can still capture turns (hooks must still be installed separately).
		if err := addExclude(root); err != nil {
			logger.Error("excluding Trace data from Git", "error", err)
		}
		_ = e.Baseline()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		ctx, cancel := context.WithCancel(ctx)
		defer stop()
		defer cancel()
		// A single worker serializes checkpoints and logs their errors; one
		// pending request is enough, so a turn cannot spawn an unbounded number
		// of goroutines each copying the trace directory.
		checkpointReq := make(chan struct{}, 1)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-checkpointReq:
					if err := gitx.Checkpoint(root); err != nil {
						logger.Error("git checkpoint", "error", err)
					}
				}
			}
		}()
		companionWorker := companions.StartDelayed(ctx, func(ctx context.Context) {
			if userstate.Disabled() || !companions.AutoEnabled(root) {
				return
			}
			if _, err := os.Stat(filepath.Join(dir, "trace.db")); err != nil {
				return
			}
			report := companions.Refresh(ctx, root)
			if err := saveCompanionSnapshot(ctx, e); err != nil {
				logger.Error("saving companion snapshot", "error", err)
			}
			if err := companions.SaveStatus(root, report); err != nil {
				logger.Error("saving companion refresh status", "error", err)
			}
		}, time.Second)
		defer companionWorker.Close()
		process := func(ev event.Event) error {
			if userstate.DisabledFor(ev.Agent) {
				return nil
			}
			err := e.Process(ev)
			if err == nil && (ev.Kind == event.TurnEnd || ev.Kind == event.SessionEnd) {
				companionWorker.Notify()
				if gitx.GitNativeEnabled(root) {
					select {
					case checkpointReq <- struct{}{}:
					default:
					}
				}
			}
			return err
		}
		sources := newSessionSources(process)
		if err := daemon.WriteDaemonIdentity(dir); err != nil {
			return err
		}
		defer daemon.RemoveDaemonIdentity(dir)
		personalReady := filepath.Join(dir, "personal-sync.ready")
		if err := daemon.WriteStateFile(personalReady, []byte(strconv.Itoa(os.Getpid()))); err != nil {
			return fmt.Errorf("marking personal sync support: %w", err)
		}
		defer os.Remove(personalReady)
		go func() {
			if err := watch.Run(ctx, root, func(paths []string) {
				if !watchedCaptureAllowed(e) {
					return
				}
				if err := e.RecordChanges(paths); err != nil {
					logger.Error("recording file changes", "error", err)
				}
			}); err != nil {
				logger.Error("watcher stopped", "error", err)
			}
		}()
		syncWake := make(chan struct{}, 1)
		go autoSyncLoop(ctx, root, e, logger, syncWake)
		// Collect expired history in the background. Wait for a collection in
		// progress before the engine closes.
		gcDone := make(chan struct{})
		go func() {
			defer close(gcDone)
			autoGCLoop(ctx, root, e, logger)
		}()
		defer func() {
			cancel()
			<-gcDone
		}()
		// Agents enabled by a marker are read from their own session files.
		for _, reader := range adapters.SessionReaders() {
			if _, err := os.Stat(filepath.Join(dir, reader.EnableMarker().File)); err != nil {
				continue
			}
			send := process
			if reader.HooksSupersedeSessions() {
				send = sources.transcript
			}
			reader.StartSessionReader(ctx, root, logger, send)
		}
		err = daemon.ServeCheckedWithLogger(ctx, dir, logger, func(ev event.Event) error {
			err := sources.hook(ev)
			if err != nil {
				logger.Error("processing event", "kind", ev.Kind, "error", err)
			} else if ev.Kind == event.TurnEnd || ev.Kind == event.SessionEnd {
				select {
				case syncWake <- struct{}{}:
				default:
				}
			}
			return err
		})
		if err != nil {
			logger.Error("daemon stopped", "error", err)
		}
		return err
	}}
	cmd.AddCommand(&cobra.Command{Use: "start", RunE: func(c *cobra.Command, _ []string) error {
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		if err := daemon.EnsureRunning(root); err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]bool{"running": true})
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), "daemon running")
		return err
	}})
	cmd.AddCommand(&cobra.Command{Use: "stop", RunE: func(c *cobra.Command, _ []string) error {
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(root, ".trace", "daemon.pid"))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading daemon pid: %w", err)
		}
		pid, err := strconv.Atoi(string(b))
		if err != nil {
			return fmt.Errorf("parsing daemon pid: %w", err)
		}
		switch daemon.CheckTraceDaemon(pid, root) {
		case daemon.DaemonAbsent:
			_ = os.Remove(filepath.Join(root, ".trace", "daemon.pid"))
			return nil
		case daemon.DaemonUnknown:
			// Deleting the pid file here would orphan a running daemon.
			return fmt.Errorf("cannot confirm that process %d is the Trace daemon; daemon.pid was kept, stop the process manually", pid)
		}
		p, err := os.FindProcess(pid)
		if err != nil {
			return err
		}
		if err := p.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("stopping daemon: %w", err)
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]bool{"stopped": true})
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), "daemon stopping")
		return err
	}})
	return cmd
}

func watchedCaptureAllowed(e *engine.Engine) bool {
	if userstate.Disabled() {
		return false
	}
	var agent string
	err := e.DB.QueryRow(`SELECT s.agent FROM turns t JOIN sessions s ON s.id=t.session_id
		WHERE s.worktree=? AND t.ended_at IS NULL ORDER BY t.started_at DESC LIMIT 1`, e.Root).Scan(&agent)
	if err == sql.ErrNoRows {
		return true
	}
	return err == nil && !userstate.AgentDisabled(agent)
}
