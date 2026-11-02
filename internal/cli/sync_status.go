package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
	"github.com/spf13/cobra"
)

func syncStatusCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show local upload progress and connection health", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(loginPath(root))
		if os.IsNotExist(err) {
			return fmt.Errorf("sync is off; run trace connect")
		}
		if err != nil {
			return err
		}
		var cfg syncclient.Config
		if err = json.Unmarshal(b, &cfg); err != nil {
			return err
		}
		h, err := syncclient.ReadHealth(repo.TraceDir(root))
		if err != nil {
			return err
		}
		if h.ConnectionID != cfg.ConnectionID {
			h = syncclient.Health{ConnectionID: cfg.ConnectionID}
		}
		var state syncclient.State
		if data, readErr := os.ReadFile(filepath.Join(repo.TraceDir(root), "sync-state.json")); readErr == nil {
			if err = json.Unmarshal(data, &state); err != nil {
				return err
			}
		} else if !os.IsNotExist(readErr) {
			return readErr
		}
		expected := cfg.Endpoint + "|" + cfg.Remote + "|" + cfg.Mode + "|v2"
		if cfg.Mode == "personal" {
			expected += "|" + cfg.ConnectionID
		}
		if state.Target != expected {
			state.Cursors = nil
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		var pending int64
		for _, table := range []string{"sessions", "turns", "keyframes", "ops", "commits", "messages", "sync_updates", "context_snapshots"} {
			if table == "context_snapshots" && cfg.Mode != "personal" {
				continue
			}
			query := "SELECT count(*) FROM " + table + " WHERE id>?"
			args := []any{state.Cursors[table]}
			if table == "commits" {
				query = "SELECT count(*) FROM commits"
				args = nil
				if state.Cursors[table] != "" {
					at, sha, ok := strings.Cut(state.Cursors[table], ":")
					if !ok {
						return fmt.Errorf("invalid commit cursor")
					}
					millis, err := strconv.ParseInt(at, 10, 64)
					if err != nil {
						return err
					}
					query += " WHERE (at,sha)>(?,?)"
					args = []any{millis, sha}
				}
			}
			var count int64
			if err = e.DB.QueryRowContext(c.Context(), query, args...).Scan(&count); err != nil {
				return err
			}
			pending += count
		}
		storeReady := true
		if runtime.GOOS == "linux" {
			_, lookupErr := exec.LookPath("secret-tool")
			storeReady = lookupErr == nil && os.Getenv("DBUS_SESSION_BUS_ADDRESS") != ""
		}
		expired := cfg.ConnectionExpiresAt > 0 && cfg.ConnectionExpiresAt <= time.Now().UnixMilli()
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"remote": cfg.Remote, "mode": cfg.Mode, "automatic": cfg.AutoSync, "pending_rows": pending, "health": h, "expired": expired, "credential_store_available": storeReady})
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Project: %s (%s)\nAutomatic sync: %t\nPending rows: %d\nLast success: %d\nLast error: %s\n", cfg.Remote, cfg.Mode, cfg.AutoSync, pending, h.LastSuccess, h.LastError)
		if expired {
			fmt.Fprintln(c.OutOrStdout(), "Connection expired: run trace connect again.")
		}
		if !storeReady {
			fmt.Fprintln(c.OutOrStdout(), "Linux sync needs secret-tool and an unlocked Secret Service in this user's D-Bus session.")
		}
		if h.Action != "" {
			fmt.Fprintln(c.OutOrStdout(), h.Action)
		}
		return err
	}}
}
