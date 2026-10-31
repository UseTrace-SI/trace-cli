package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/spf13/cobra"
)

type statsReport struct {
	Root           string            `json:"root"`
	Sessions       int64             `json:"sessions"`
	Turns          int64             `json:"turns"`
	Keyframes      int64             `json:"keyframes"`
	Ops            int64             `json:"ops"`
	Messages       int64             `json:"messages"`
	Commits        int64             `json:"commits"`
	Agents         map[string]int64  `json:"agents"`
	OldestStep     int64             `json:"oldest_step,omitempty"`
	NewestStep     int64             `json:"newest_step,omitempty"`
	StorageBytes   int64             `json:"storage_bytes"`
	TokensIn       int64             `json:"tokens_in"`
	TokensOut      int64             `json:"tokens_out"`
}

func statsCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "stats", Short: "Summarize local Trace history", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		report := statsReport{Root: root, Agents: map[string]int64{}}
		counts := []struct {
			table string
			dest  *int64
		}{
			{"sessions", &report.Sessions},
			{"turns", &report.Turns},
			{"keyframes", &report.Keyframes},
			{"ops", &report.Ops},
			{"messages", &report.Messages},
			{"commits", &report.Commits},
		}
		for _, item := range counts {
			if err := e.DB.QueryRow("SELECT COUNT(*) FROM " + item.table).Scan(item.dest); err != nil {
				return fmt.Errorf("counting %s: %w", item.table, err)
			}
		}
		rows, err := e.DB.Query("SELECT agent, COUNT(*) FROM sessions GROUP BY agent")
		if err != nil {
			return fmt.Errorf("counting agents: %w", err)
		}
		for rows.Next() {
			var agent string
			var n int64
			if err := rows.Scan(&agent, &n); err != nil {
				rows.Close()
				return err
			}
			report.Agents[agent] = n
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if err := e.DB.QueryRow("SELECT COALESCE(MIN(seq),0),COALESCE(MAX(seq),0) FROM keyframes").Scan(&report.OldestStep, &report.NewestStep); err != nil {
			return fmt.Errorf("reading step range: %w", err)
		}
		if err := e.DB.QueryRow("SELECT COALESCE(SUM(tokens_in),0),COALESCE(SUM(tokens_out),0) FROM turns").Scan(&report.TokensIn, &report.TokensOut); err != nil {
			return fmt.Errorf("reading token totals: %w", err)
		}
		// storage size
		traceDir := repo.TraceDir(root)
		filepath.Walk(traceDir, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				report.StorageBytes += info.Size()
			}
			return nil
		})
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(report)
		}
		if _, err := fmt.Fprintf(c.OutOrStdout(), "Trace stats for %s\n", root); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.OutOrStdout(), "  sessions:   %d\n  turns:      %d\n  keyframes:  %d\n  ops:        %d\n  messages:   %d\n  commits:    %d\n  tokens:     %d in / %d out\n", report.Sessions, report.Turns, report.Keyframes, report.Ops, report.Messages, report.Commits, report.TokensIn, report.TokensOut); err != nil {
			return err
		}
		if len(report.Agents) > 0 {
			if _, err := fmt.Fprintln(c.OutOrStdout(), "  agents:"); err != nil {
				return err
			}
			for agent, n := range report.Agents {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "    %-12s %d\n", agent, n); err != nil {
					return err
				}
			}
		}
		if report.NewestStep > 0 {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "  steps:      %d..%d\n", report.OldestStep, report.NewestStep); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(c.OutOrStdout(), "  storage:    %d bytes\n", report.StorageBytes); err != nil {
			return err
		}
		return nil
	}}
	return cmd
}
