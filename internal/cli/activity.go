package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

type activityAgent struct {
	Agent string `json:"agent"`
	Turns int    `json:"turns"`
}

type activitySession struct {
	ID       string `json:"id"`
	Agent    string `json:"agent"`
	Turns    int    `json:"turns"`
	LastStep int64  `json:"last_step"`
	LastAt   int64  `json:"last_at"`
}

type activityReport struct {
	Since          int64             `json:"since"`
	Turns          int               `json:"turns"`
	Sessions       int               `json:"sessions"`
	FilesChanged   int               `json:"files_changed"`
	Agents         []activityAgent   `json:"agents"`
	RecentSessions []activitySession `json:"recent_sessions"`
}

func activityCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var days int
	cmd := &cobra.Command{Use: "activity", Short: "Summarize local recorded activity", RunE: func(c *cobra.Command, _ []string) error {
		if days < 1 {
			return fmt.Errorf("days must be positive")
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
		report, err := localActivity(e, time.Now().AddDate(0, 0, -days).UnixMilli())
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(report)
		}
		if _, err := fmt.Fprintf(c.OutOrStdout(), "Last %d days: %d turns, %d sessions, %d files changed\n", days, report.Turns, report.Sessions, report.FilesChanged); err != nil {
			return err
		}
		for _, agent := range report.Agents {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "  %-12s %d turns\n", agent.Agent, agent.Turns); err != nil {
				return err
			}
		}
		if len(report.RecentSessions) > 0 {
			if _, err := fmt.Fprintln(c.OutOrStdout(), "Recent sessions:"); err != nil {
				return err
			}
			for _, session := range report.RecentSessions {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "  %s  %-12s %d turns  last step %d\n", session.ID, session.Agent, session.Turns, session.LastStep); err != nil {
					return err
				}
			}
		}
		return nil
	}}
	cmd.Flags().IntVar(&days, "days", 7, "include activity from the last N days")
	return cmd
}

func localActivity(e *engine.Engine, since int64) (activityReport, error) {
	report := activityReport{Since: since, Agents: []activityAgent{}, RecentSessions: []activitySession{}}
	rows, err := e.DB.Query(`SELECT s.agent,COUNT(*) FROM keyframes k JOIN turns t ON t.id=k.turn_id
		JOIN sessions s ON s.id=t.session_id WHERE k.tag='turn' AND k.created_at>=?
		GROUP BY s.agent ORDER BY COUNT(*) DESC,s.agent`, since)
	if err != nil {
		return report, fmt.Errorf("counting activity by agent: %w", err)
	}
	for rows.Next() {
		var agent activityAgent
		if err := rows.Scan(&agent.Agent, &agent.Turns); err != nil {
			rows.Close()
			return report, fmt.Errorf("reading agent activity: %w", err)
		}
		report.Agents = append(report.Agents, agent)
		report.Turns += agent.Turns
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	rows.Close()
	if err := e.DB.QueryRow(`SELECT COUNT(DISTINCT t.session_id) FROM keyframes k JOIN turns t ON t.id=k.turn_id
		WHERE k.tag='turn' AND k.created_at>=?`, since).Scan(&report.Sessions); err != nil {
		return report, fmt.Errorf("counting active sessions: %w", err)
	}
	if err := e.DB.QueryRow(`SELECT COUNT(DISTINCT o.path) FROM ops o JOIN turns t ON t.id=o.turn_id
		JOIN keyframes k ON k.turn_id=t.id WHERE k.tag='turn' AND k.created_at>=? AND o.path!=''`, since).Scan(&report.FilesChanged); err != nil {
		return report, fmt.Errorf("counting changed files: %w", err)
	}
	rows, err = e.DB.Query(`SELECT s.id,s.agent,COUNT(*),MAX(k.seq),MAX(k.created_at)
		FROM keyframes k JOIN turns t ON t.id=k.turn_id JOIN sessions s ON s.id=t.session_id
		WHERE k.tag='turn' AND k.created_at>=? GROUP BY s.id,s.agent
		ORDER BY MAX(k.created_at) DESC,s.id LIMIT 10`, since)
	if err != nil {
		return report, fmt.Errorf("listing recent sessions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var session activitySession
		if err := rows.Scan(&session.ID, &session.Agent, &session.Turns, &session.LastStep, &session.LastAt); err != nil {
			return report, fmt.Errorf("reading recent session: %w", err)
		}
		report.RecentSessions = append(report.RecentSessions, session)
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	return report, nil
}
