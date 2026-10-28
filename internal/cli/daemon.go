package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/codex"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/watch"
)

func daemonCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "daemon", Hidden: true, RunE: func(_ *cobra.Command, _ []string) error {
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		dir := filepath.Join(root, ".trace")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating trace directory: %w", err)
		}
		logfile, err := os.OpenFile(filepath.Join(dir, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("opening daemon log: %w", err)
		}
		defer logfile.Close()
		logger := slog.New(slog.NewTextHandler(logfile, nil))
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		ctx, cancel := context.WithCancel(ctx)
		defer stop()
		defer cancel()
		timer := time.AfterFunc(30*time.Minute, cancel)
		defer timer.Stop()
		pidPath := filepath.Join(dir, "daemon.pid")
		if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			return fmt.Errorf("writing pid file: %w", err)
		}
		defer os.Remove(pidPath)
		go func() {
			if err := watch.Run(ctx, root, func(paths []string) {
				if err := e.RecordChanges(paths); err != nil {
					logger.Error("recording file changes", "error", err)
				}
			}); err != nil {
				logger.Error("watcher stopped", "error", err)
			}
		}()
		if _, err := os.Stat(filepath.Join(dir, "codex.enabled")); err == nil {
			home, err := os.UserHomeDir()
			if err == nil {
				go func() {
					if err := codex.Run(ctx, root, filepath.Join(home, ".codex", "sessions"), e.Process); err != nil {
						logger.Error("Codex adapter stopped", "error", err)
					}
				}()
			}
		}
		err = daemon.Serve(ctx, dir, func(ev event.Event) {
			timer.Reset(30 * time.Minute)
			if err := e.Process(ev); err != nil {
				logger.Error("processing event", "kind", ev.Kind, "error", err)
			}
		})
		if err != nil {
			logger.Error("daemon stopped", "error", err)
		}
		return err
	}}
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
