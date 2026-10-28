package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

type updateState struct {
	CheckedAt int64  `json:"checked_at"`
	Latest    string `json:"latest"`
}

func updateCommand(repoPath *string, jsonOutput *bool, current string) *cobra.Command {
	return &cobra.Command{Use: "update-check", Short: "Check for a newer release (opt-in)", RunE: func(c *cobra.Command, _ []string) error {
		if os.Getenv("TRACE_NO_UPDATE_CHECK") == "1" {
			return fmt.Errorf("update checks are disabled")
		}
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		p := filepath.Join(root, ".trace", "update-check.json")
		var state updateState
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &state)
		}
		if time.Since(time.UnixMilli(state.CheckedAt)) >= 24*time.Hour || state.Latest == "" {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/UseTrace-SI/trace-cli/releases/latest", nil)
			if err != nil {
				return err
			}
			req.Header.Set("Accept", "application/vnd.github+json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("checking releases: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("release check returned %s", resp.Status)
			}
			var release struct {
				TagName string `json:"tag_name"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
				return err
			}
			state = updateState{CheckedAt: time.Now().UnixMilli(), Latest: release.TagName}
			b, err := json.Marshal(state)
			if err != nil {
				return err
			}
			if err := os.WriteFile(p, b, 0600); err != nil {
				return err
			}
		}
		out := map[string]any{"current": current, "latest": state.Latest, "update_available": state.Latest != "" && state.Latest != "v"+current && state.Latest != current}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(out)
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Current: %s\nLatest: %s\n", current, state.Latest)
		return err
	}}
}
