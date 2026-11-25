package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
	"github.com/spf13/cobra"
)

// spoolDropWarnWindow is how long trace doctor reports dropped hook events.
const spoolDropWarnWindow = 7 * 24 * time.Hour

func doctorCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var bundle string
	cmd := &cobra.Command{Use: "doctor", RunE: func(c *cobra.Command, _ []string) error {
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		type check struct {
			Name   string `json:"name"`
			OK     bool   `json:"ok"`
			Detail string `json:"detail,omitempty"`
		}
		var checks []check
		add := func(name string, ok bool, detail string) {
			checks = append(checks, check{Name: name, OK: ok, Detail: detail})
		}

		// git repo
		gr, gerr := repo.FindRoot(root)
		if gerr != nil {
			add("git repository", false, gerr.Error())
		} else {
			add("git repository", true, gr)
		}

		td := repo.TraceDir(root)
		// .trace dir
		if info, err := os.Stat(td); err != nil || !info.IsDir() {
			add("trace directory", false, "run: trace init")
		} else {
			add("trace directory", true, td)
		}
		// trace.db
		if _, err := os.Stat(filepath.Join(td, "trace.db")); err != nil {
			add("trace database", false, "missing; run: trace init")
		} else {
			add("trace database", true, "")
		}
		// exclude
		excludePath, _ := gitExcludePath(root)
		excl, _ := os.ReadFile(excludePath)
		if strings.Contains(string(excl), ".trace/") {
			add(".trace in git exclude", true, "")
		} else {
			add(".trace in git exclude", false, "run: trace init")
		}
		// daemon
		daemonUp, daemonUnverified := false, false
		pidBytes, _ := os.ReadFile(filepath.Join(td, "daemon.pid"))
		if len(pidBytes) > 0 {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes))); err == nil {
				switch daemon.CheckTraceDaemon(pid, root) {
				case daemon.DaemonConfirmed:
					daemonUp = true
				case daemon.DaemonUnknown:
					daemonUnverified = true
				}
			}
		}
		add("daemon running", daemonUp, func() string {
			if daemonUp {
				return "pid " + strings.TrimSpace(string(pidBytes))
			}
			if daemonUnverified {
				return "pid " + strings.TrimSpace(string(pidBytes)) + " is running but could not be identified"
			}
			return "run: trace daemon start (or any hook will auto-start it)"
		}())
		// Any supported adapter is enough: an enable marker or a file that
		// shows the agent's project integration.
		hooksOK := false
		for _, agent := range adapters.Markers() {
			if _, err := os.Stat(filepath.Join(td, agent.EnableMarker().File)); err == nil {
				hooksOK = true
			}
		}
		for _, agent := range adapters.All() {
			evidencer, ok := agent.(adapters.ProjectEvidencer)
			if !ok {
				continue
			}
			for _, evidence := range evidencer.InstallEvidence() {
				if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(evidence.Path))); err == nil && strings.Contains(string(b), evidence.Contains) {
					hooksOK = true
				}
			}
		}
		add("agent integration installed", hooksOK, func() string {
			if hooksOK {
				return ""
			}
			return "run: trace init --agent NAME"
		}())
		// daemon log watcher errors
		logBytes, _ := os.ReadFile(filepath.Join(td, "daemon.log"))
		if strings.Contains(string(logBytes), "watcher stopped") {
			add("file watcher", false, "watcher stopped; check daemon.log (often EMFILE)")
		} else {
			add("file watcher", true, "")
		}
		// size
		size := traceDirSize(td)
		if budget, err := maxStoreBytes(); err != nil {
			add(".trace size", false, err.Error())
		} else if size > budget {
			add(".trace size", false, fmt.Sprintf("%d bytes exceeds the %d byte budget (TRACE_MAX_STORE_BYTES); run: trace gc", size, budget))
		} else {
			add(".trace size", true, fmt.Sprintf("%d bytes (budget %d)", size, budget))
		}
		// An unusable redaction key makes each process mark secrets with its
		// own ephemeral key, so markers stop matching across processes.
		if path, err := redact.CheckMarkerKey(); err != nil {
			add("redaction key", false, err.Error())
		} else {
			add("redaction key", true, path)
		}
		// Hooks drop events rather than block when the spool is full.
		spool := daemon.ReadSpoolHealth(td)
		switch {
		case spool.Dropped > 0 && time.Since(spool.LastDrop) < spoolDropWarnWindow:
			add("event spool", false, fmt.Sprintf("%d hook events dropped (last %s) because the spool reached %d bytes; check that the daemon is running", spool.Dropped, spool.LastDrop.Format(time.RFC3339), spool.Limit))
		case spool.Bytes > spool.Limit/4*3:
			add("event spool", false, fmt.Sprintf("%d bytes waiting, near the %d byte limit; check that the daemon is running", spool.Bytes, spool.Limit))
		default:
			add("event spool", true, fmt.Sprintf("%d bytes waiting", spool.Bytes))
		}

		if _, err := readSyncConfig(root); err == nil {
			health, readErr := syncclient.ReadHealth(td)
			add("sync health", readErr == nil && health.LastError == "", "Inspect progress with: trace sync status")
		}
		allOK := true
		for _, ch := range checks {
			if !ch.OK {
				allOK = false
			}
		}
		if bundle != "" {
			bundleData := map[string]any{
				"ok":     allOK,
				"checks": checks,
			}
			b, err := json.MarshalIndent(bundleData, "", "  ")
			if err != nil {
				return fmt.Errorf("encoding diagnostic bundle: %w", err)
			}
			if err := os.WriteFile(bundle, b, 0600); err != nil {
				return fmt.Errorf("writing bundle: %w", err)
			}
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"ok": allOK, "checks": checks, "bundle": bundle})
		}
		for _, ch := range checks {
			mark := "✓"
			if !ch.OK {
				mark = "✗"
			}
			if ch.Detail != "" {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "%s %-28s %s\n", mark, ch.Name, ch.Detail); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintf(c.OutOrStdout(), "%s %s\n", mark, ch.Name); err != nil {
				return err
			}
		}
		if bundle != "" {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "Wrote diagnostic bundle to %s\n", bundle); err != nil {
				return err
			}
		}
		if !allOK && bundle == "" {
			return fmt.Errorf("doctor found issues")
		}
		return nil
	}}
	cmd.Flags().StringVar(&bundle, "bundle", "", "write diagnostic checks without log contents")
	return cmd
}
