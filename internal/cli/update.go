package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://usetrace.si/stack.json", nil)
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
				Version string `json:"version"`
			}
			if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&release); err != nil {
				return err
			}
			if !validVersion(release.Version) {
				return fmt.Errorf("invalid hosted stack version")
			}
			state = updateState{CheckedAt: time.Now().UnixMilli(), Latest: release.Version}
			b, err := json.Marshal(state)
			if err != nil {
				return err
			}
			if err := os.WriteFile(p, b, 0600); err != nil {
				return err
			}
		}
		out := map[string]any{"current": current, "latest": state.Latest, "update_available": newerVersion(state.Latest, current)}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(out)
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Current: %s\nLatest: %s\n", current, state.Latest)
		return err
	}}
}

func validVersion(version string) bool {
	core, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return false
		}
	}
	return true
}
func newerVersion(latest, current string) bool {
	if !validVersion(latest) || !validVersion(current) {
		return false
	}
	a, ap, _ := strings.Cut(strings.TrimPrefix(latest, "v"), "-")
	b, bp, _ := strings.Cut(strings.TrimPrefix(current, "v"), "-")
	av, bv := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		x, _ := strconv.Atoi(av[i])
		y, _ := strconv.Atoi(bv[i])
		if x != y {
			return x > y
		}
	}
	if ap == bp {
		return false
	}
	if ap == "" {
		return true
	}
	if bp == "" {
		return false
	}
	ax, bx := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(ax) && i < len(bx); i++ {
		if ax[i] == bx[i] {
			continue
		}
		x, xe := strconv.Atoi(ax[i])
		y, ye := strconv.Atoi(bx[i])
		if xe == nil && ye == nil {
			return x > y
		}
		if xe == nil {
			return false
		}
		if ye == nil {
			return true
		}
		return ax[i] > bx[i]
	}
	return len(ax) > len(bx)
}
