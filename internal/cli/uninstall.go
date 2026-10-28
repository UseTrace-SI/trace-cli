package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/agy"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/claudecode"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/cursor"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/gemini"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/gitx"
	"github.com/spf13/cobra"
)

func uninstallCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var purge bool
	cmd := &cobra.Command{Use: "uninstall", RunE: func(c *cobra.Command, _ []string) error {
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		if err := claudecode.Uninstall(root); err != nil {
			return err
		}
		if err := cursor.Uninstall(root); err != nil {
			return err
		}
		if err := agy.Uninstall(root); err != nil {
			return err
		}
		if err := gemini.Uninstall(root); err != nil {
			return err
		}
		if err := uninstallMCP(root); err != nil {
			return err
		}
		if err := gitx.UninstallHook(root); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(root, ".trace", "codex.enabled"))
		pidBytes, err := os.ReadFile(filepath.Join(root, ".trace", "daemon.pid"))
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes))); err == nil && daemon.ProcessIsTraceDaemon(pid, root) {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Signal(syscall.SIGTERM)
					// Wait for the daemon to release the SQLite database and
					// remove its PID file before deleting .trace.
					deadline := time.Now().Add(5 * time.Second)
					for daemon.ProcessRunning(pid) && time.Now().Before(deadline) {
						time.Sleep(25 * time.Millisecond)
					}
					if daemon.ProcessRunning(pid) {
						_ = p.Signal(syscall.SIGKILL)
						for i := 0; i < 100 && daemon.ProcessRunning(pid); i++ {
							time.Sleep(10 * time.Millisecond)
						}
					}
				}
			}
		}
		if purge {
			if err := os.RemoveAll(filepath.Join(root, ".trace")); err != nil {
				return fmt.Errorf("purging Trace data: %w", err)
			}
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]bool{"uninstalled": true, "purged": purge})
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), "Trace hooks removed")
		return err
	}}
	cmd.Flags().BoolVar(&purge, "purge", false, "delete local Trace history")
	return cmd
}
