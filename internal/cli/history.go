package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/restore"
)

type logRow struct {
	Step         int64  `json:"step"`
	Time         int64  `json:"time"`
	Agent        string `json:"agent"`
	Prompt       string `json:"prompt"`
	FilesChanged int    `json:"files_changed"`
	Tag          string `json:"tag"`
}

func logCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{Use: "log", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		rows, err := e.DB.Query(`WITH changed AS (
			SELECT turn_id,COUNT(DISTINCT path) AS files_changed FROM ops
			WHERE path!='' AND (before_hash IS NOT NULL OR after_hash IS NOT NULL) GROUP BY turn_id)
			SELECT k.seq,k.created_at,COALESCE(s.agent,''),COALESCE(t.prompt,''),COALESCE(k.tag,''),COALESCE(changed.files_changed,0)
			FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id LEFT JOIN sessions s ON s.id=t.session_id
			LEFT JOIN changed ON changed.turn_id=t.id ORDER BY k.seq`)
		if err != nil {
			return fmt.Errorf("querying history: %w", err)
		}
		defer rows.Close()
		var out []logRow
		for rows.Next() {
			var r logRow
			if err := rows.Scan(&r.Step, &r.Time, &r.Agent, &r.Prompt, &r.Tag, &r.FilesChanged); err != nil {
				return fmt.Errorf("reading history: %w", err)
			}
			if len(r.Prompt) > 60 {
				r.Prompt = r.Prompt[:60]
			}
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("reading history: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("closing history query: %w", err)
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(out)
		}
		for _, r := range out {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "%d\t%s\t%s\t%d files\t%s\n", r.Step, time.UnixMilli(r.Time).Format(time.RFC3339), r.Agent, r.FilesChanged, r.Prompt); err != nil {
				return err
			}
		}
		return nil
	}}
}

func blobText(bs *blob.Store, h blob.Hash) (string, error) {
	if h == "" {
		return "", nil
	}
	r, err := bs.Get(h)
	if err != nil {
		return "", err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unifiedDiff(path, before, after string) string {
	if before == after {
		return ""
	}
	a := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	b := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	if before == "" {
		a = nil
	}
	if after == "" {
		b = nil
	}
	var s strings.Builder
	fmt.Fprintf(&s, "--- a/%s\n+++ b/%s\n@@ -1,%d +1,%d @@\n", path, path, len(a), len(b))
	if len(a) > 2000 || len(b) > 2000 {
		for _, line := range a {
			fmt.Fprintf(&s, "-%s\n", line)
		}
		for _, line := range b {
			fmt.Fprintf(&s, "+%s\n", line)
		}
		return s.String()
	}
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	for i, j := 0, 0; i < len(a) || j < len(b); {
		if i < len(a) && j < len(b) && a[i] == b[j] {
			fmt.Fprintf(&s, " %s\n", a[i])
			i++
			j++
		} else if i < len(a) && (j == len(b) || dp[i+1][j] >= dp[i][j+1]) {
			fmt.Fprintf(&s, "-%s\n", a[i])
			i++
		} else {
			fmt.Fprintf(&s, "+%s\n", b[j])
			j++
		}
	}
	return s.String()
}

func showCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{Use: "show <step>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		seq, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid step: %w", err)
		}
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		var prompt, reasoning, tools, agent string
		var tag string
		err = e.DB.QueryRow(`SELECT COALESCE(t.prompt,''),COALESCE(t.reasoning,''),COALESCE(t.tool_calls,''),COALESCE(s.agent,''),COALESCE(k.tag,'')
			FROM keyframes k LEFT JOIN turns t ON t.id=k.turn_id LEFT JOIN sessions s ON s.id=t.session_id WHERE k.seq=?`, seq).Scan(&prompt, &reasoning, &tools, &agent, &tag)
		if err != nil {
			return fmt.Errorf("finding step: %w", err)
		}
		symbolRows, err := e.DB.Query(`SELECT DISTINCT detail FROM ops WHERE turn_id=(SELECT turn_id FROM keyframes WHERE seq=?)
			AND detail IS NOT NULL AND detail!='' ORDER BY detail`, seq)
		if err != nil {
			return fmt.Errorf("finding changed symbols: %w", err)
		}
		var symbols []string
		for symbolRows.Next() {
			var symbol string
			if err := symbolRows.Scan(&symbol); err != nil {
				symbolRows.Close()
				return err
			}
			symbols = append(symbols, symbol)
		}
		if err := symbolRows.Err(); err != nil {
			symbolRows.Close()
			return err
		}
		if err := symbolRows.Close(); err != nil {
			return err
		}
		current, err := e.ManifestAt(seq)
		if err != nil {
			return err
		}
		previous := manifest.Manifest{}
		var prevSeq int64
		if err := e.DB.QueryRow("SELECT seq FROM keyframes WHERE seq<? ORDER BY seq DESC LIMIT 1", seq).Scan(&prevSeq); err == nil {
			previous, err = e.ManifestAt(prevSeq)
			if err != nil {
				return err
			}
		} else if err != sql.ErrNoRows {
			return err
		}
		files := restore.Plan(previous, current)
		var diff strings.Builder
		for _, p := range files {
			oldText, err := blobText(e.Blobs, previous[p].Hash)
			if err != nil {
				return err
			}
			newText, err := blobText(e.Blobs, current[p].Hash)
			if err != nil {
				return err
			}
			diff.WriteString(unifiedDiff(p, oldText, newText))
		}
		diffText := (redact.Redactor{}).Text(diff.String())
		result := map[string]any{"step": seq, "tag": tag, "agent": agent, "prompt": prompt, "reasoning": reasoning, "tool_calls": tools, "files": files, "symbols": symbols, "diff": diffText}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(result)
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Step %d (%s, %s)\nPrompt: %s\nReasoning: %s\nTools: %s\n", seq, tag, agent, prompt, reasoning, tools)
		if err != nil {
			return err
		}
		if len(symbols) > 0 {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "Symbols: %s\n", strings.Join(symbols, ", ")); err != nil {
				return err
			}
		}
		_, err = fmt.Fprint(c.OutOrStdout(), diffText)
		return err
	}}
}

func rewindCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var glob string
	var dryRun bool
	cmd := &cobra.Command{Use: "rewind <step>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		seq, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid step: %w", err)
		}
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		safety, changed, err := e.Rewind(seq, glob, dryRun)
		if err != nil {
			return err
		}
		result := map[string]any{"step": seq, "safety_step": safety, "changed": changed, "dry_run": dryRun}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(result)
		}
		for _, p := range changed {
			if _, err := fmt.Fprintln(c.OutOrStdout(), p); err != nil {
				return err
			}
		}
		if safety > 0 {
			_, err = fmt.Fprintf(c.OutOrStdout(), "Undo with: trace rewind %d\n", safety)
		}
		return err
	}}
	cmd.Flags().StringVar(&glob, "files", "", "restore only paths matching glob")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show changes without writing")
	return cmd
}

func statusCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{Use: "status", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		dir := filepath.Join(root, ".trace")
		conn, dialErr := net.DialTimeout("unix", filepath.Join(dir, "sock"), 5*time.Millisecond)
		up := dialErr == nil || daemon.SpoolReady(dir)
		if dialErr == nil {
			conn.Close()
		}
		var active int
		if err := e.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE ended_at IS NULL").Scan(&active); err != nil {
			return err
		}
		var latest int64
		if err := e.DB.QueryRow("SELECT COALESCE(MAX(seq),0) FROM keyframes").Scan(&latest); err != nil {
			return err
		}
		var size int64
		err = filepath.WalkDir(dir, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !d.IsDir() {
				info, err := d.Info()
				if err != nil {
					return err
				}
				size += info.Size()
			}
			return nil
		})
		if err != nil {
			return err
		}
		result := map[string]any{"daemon_up": up, "active_sessions": active, "latest_step": latest, "size_bytes": size}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(result)
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Daemon: %t\nActive sessions: %d\nLatest step: %d\nTrace size: %d bytes\n", up, active, latest, size)
		return err
	}}
}
