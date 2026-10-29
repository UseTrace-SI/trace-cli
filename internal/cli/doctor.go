package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/spf13/cobra"
)

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
		daemonUp := false
		pidBytes, _ := os.ReadFile(filepath.Join(td, "daemon.pid"))
		if len(pidBytes) > 0 {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes))); err == nil {
				if daemon.ProcessIsTraceDaemon(pid, root) {
					daemonUp = true
				}
			}
		}
		add("daemon running", daemonUp, func() string {
			if daemonUp {
				return "pid " + strings.TrimSpace(string(pidBytes))
			}
			return "run: trace daemon start (or any hook will auto-start it)"
		}())
		// Any supported adapter is enough; Claude hooks are optional.
		hooksOK := false
		hooksPath := filepath.Join(root, ".claude", "settings.local.json")
		if b, err := os.ReadFile(hooksPath); err == nil {
			if strings.Contains(string(b), " hook claude-code Stop") {
				hooksOK = true
			}
		}
		for _, name := range []string{"codex.enabled", "opencode.enabled"} {
			if _, err := os.Stat(filepath.Join(td, name)); err == nil {
				hooksOK = true
			}
		}
		for path, marker := range map[string]string{
			filepath.Join(root, ".cline", "hooks", "TaskComplete"):                    "Trace Cline CLI hook",
			filepath.Join(root, ".openclaude", "settings.local.json"):                 " hook openclaude",
			filepath.Join(root, ".pi", "extensions", "trace", "index.ts"):             "Trace coding-agent extension",
			filepath.Join(root, ".omp", "extensions", "trace", "index.ts"):            "Trace coding-agent extension",
			filepath.Join(root, ".grok", "hooks", "trace.json"):                       " hook grok",
			filepath.Join(root, ".agents", "plugins", "trace", "hooks", "hooks.json"): " hook goose",
			filepath.Join(root, ".amp", "plugins", "trace.ts"):                        "Trace Amp plugin",
			filepath.Join(root, ".kilo", "plugin", "trace.ts"):                        "Trace Kilo plugin",
			filepath.Join(root, ".kiro", "hooks", "trace.json"):                       "Trace managed hook",
			filepath.Join(root, ".cursor", "hooks.json"):                              " hook cursor",
			filepath.Join(root, ".agents", "hooks.json"):                              " hook agy",
			filepath.Join(root, ".devin", "hooks.json"):                               " hook windsurf",
			filepath.Join(root, ".windsurf", "hooks.json"):                            " hook windsurf",
			filepath.Join(root, ".gemini", "settings.json"):                           " hook gemini",
			filepath.Join(root, ".github", "hooks", "usetrace.json"):                  " hook copilot",
			filepath.Join(root, ".qwen", "settings.json"):                             " hook qwen",
			filepath.Join(root, ".factory", "hooks.json"):                             " hook factory",
			filepath.Join(root, ".factory", "settings.json"):                          " hook factory",
		} {
			if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), marker) {
				hooksOK = true
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
		var size int64
		_ = filepath.WalkDir(td, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, _ := d.Info()
			if info != nil {
				size += info.Size()
			}
			return nil
		})
		add(".trace size", true, fmt.Sprintf("%d bytes", size))

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
