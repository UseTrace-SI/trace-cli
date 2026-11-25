package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/gitx"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/spf13/cobra"
)

func addExclude(root string) error {
	p, err := gitExcludePath(root)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading git exclude: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == ".trace/" {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating git info directory: %w", err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("opening git exclude: %w", err)
	}
	defer f.Close()
	prefix := []byte{}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte{'\n'}) {
		prefix = []byte{'\n'}
	}
	if _, err := f.Write(append(prefix, []byte(".trace/\n")...)); err != nil {
		return fmt.Errorf("writing git exclude: %w", err)
	}
	return nil
}

func gitExcludePath(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		return "", fmt.Errorf("finding git exclude: %w", err)
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return p, nil
}

// initializationComplete verifies the artifacts a successful init leaves
// behind. A bare trace.db is not enough: an interrupted earlier run may have
// skipped hooks, the Git exclude or the daemon.
func initializationComplete(root, agent string) bool {
	traceDir := repo.TraceDir(root)
	for _, name := range []string{"trace.db", "init-complete", "daemon.pid"} {
		if _, err := os.Stat(filepath.Join(traceDir, name)); err != nil {
			return false
		}
	}
	marker, err := os.ReadFile(filepath.Join(traceDir, "init-complete"))
	if err != nil || strings.TrimSpace(string(marker)) != agent {
		return false
	}
	exclude, err := gitExcludePath(root)
	if err != nil {
		return false
	}
	b, err := os.ReadFile(exclude)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == ".trace/" {
			return true
		}
	}
	return false
}

func initCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var agent string
	var ifNeeded bool
	cmd := &cobra.Command{Use: "init", RunE: func(c *cobra.Command, _ []string) error {
		adapter, ok := adapters.ForProject(agent)
		if !ok {
			return fmt.Errorf("unsupported agent %q", agent)
		}
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		if _, err := repo.FindRoot(root); err != nil {
			return err
		}
		if ifNeeded {
			if initializationComplete(root, agent) {
				if *jsonOutput {
					return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"initialized": false, "already": true, "repo": root})
				}
				_, err = fmt.Fprintf(c.OutOrStdout(), "Trace already initialized in %s.\n", root)
				return err
			}
		}
		if checker, ok := adapter.(adapters.ProjectChecker); ok {
			if err := checker.CheckProject(root); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(repo.TraceDir(root), 0700); err != nil {
			return fmt.Errorf("creating trace directory: %w", err)
		}
		if err := addExclude(root); err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		// Create the per-install redaction key now, not in an agent's hook.
		redact.EnsureMarkerKey()
		if m, ok := adapter.(adapters.MarkerEnabled); ok {
			marker := m.EnableMarker()
			if err := os.WriteFile(filepath.Join(repo.TraceDir(root), marker.File), []byte("1\n"), 0600); err != nil {
				return fmt.Errorf("enabling %s adapter: %w", marker.Label, err)
			}
		} else if err := adapter.(adapters.ProjectInstaller).InstallProject(root); err != nil {
			return err
		}
		if err := e.Baseline(); err != nil {
			return err
		}
		if err := installMCP(root); err != nil {
			return err
		}
		var manualHook *gitx.ManualHookError
		if err := gitx.InstallHook(root); err != nil && !errors.As(err, &manualHook) {
			return err
		}
		if err := daemon.EnsureRunning(root); err != nil {
			return err
		}
		// Written last: --if-needed relies on it to recognise a complete setup.
		if err := os.WriteFile(filepath.Join(repo.TraceDir(root), "init-complete"), []byte(agent+"\n"), 0600); err != nil {
			return fmt.Errorf("marking initialization complete: %w", err)
		}
		var label, notice string
		if n, ok := adapter.(adapters.ProjectNoticer); ok {
			label, notice = n.ProjectNotice()
		}
		if *jsonOutput {
			result := map[string]any{"initialized": true, "repo": root}
			if notice != "" {
				result["additional_setup"] = notice
			}
			if manualHook != nil {
				result["git_hook_setup"] = manualHook.Hooks
			}
			return json.NewEncoder(c.OutOrStdout()).Encode(result)
		}
		if manualHook != nil {
			if _, err := fmt.Fprintln(c.OutOrStdout(), "Git runs hooks from inside this repository, so Trace left them unchanged."); err != nil {
				return err
			}
			for _, hook := range manualHook.Hooks {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "To link commits and rewrites to steps, add these lines to %s:\n%s", hook.Path, hook.Block); err != nil {
					return err
				}
			}
		}
		if notice != "" {
			_, err = fmt.Fprintf(c.OutOrStdout(), "Trace initialized. %s hooks installed. %s\n", label, notice)
			return err
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Trace initialized. %s turns will be recorded here.\n", agent)
		return err
	}}
	cmd.Flags().StringVar(&agent, "agent", "claude-code", "agent to integrate")
	cmd.Flags().BoolVar(&ifNeeded, "if-needed", false, "no-op if Trace is already initialized (safe for scripts/agents)")
	return cmd
}
