package cli

import (
	"encoding/json"
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func messagesCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var step int64
	var session string
	var limit, offset int
	cmd := &cobra.Command{Use: "messages", RunE: func(c *cobra.Command, _ []string) error {
		if c.Flags().Changed("step") && step < 1 {
			return fmt.Errorf("step must be positive")
		}
		if limit < 0 || offset < 0 {
			return fmt.Errorf("limit and offset must be nonnegative")
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
		maxRows := limit
		if maxRows == 0 {
			maxRows = -1
		}
		rows, err := e.DB.Query(`SELECT m.role,COALESCE(m.content,''),m.at,COALESCE(s.agent,''),
			COALESCE(k.seq,0),COALESCE(m.session_id,'')
			FROM messages m LEFT JOIN sessions s ON s.id=m.session_id
			LEFT JOIN keyframes k ON k.turn_id=m.turn_id
			WHERE (?<0 OR k.seq=?) AND (?='' OR m.session_id=?)
			ORDER BY m.at,m.rowid LIMIT ? OFFSET ?`, step, step, session, session, maxRows, offset)
		if err != nil {
			return fmt.Errorf("querying messages: %w", err)
		}
		defer rows.Close()
		type msg struct {
			Role      string `json:"role"`
			Agent     string `json:"agent,omitempty"`
			Content   string `json:"content"`
			At        int64  `json:"at"`
			Step      int64  `json:"step,omitempty"`
			SessionID string `json:"session_id,omitempty"`
		}
		out := make([]msg, 0)
		for rows.Next() {
			var m msg
			if err := rows.Scan(&m.Role, &m.Content, &m.At, &m.Agent, &m.Step, &m.SessionID); err != nil {
				return fmt.Errorf("reading message: %w", err)
			}
			out = append(out, m)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(out)
		}
		for _, m := range out {
			stepLabel := ""
			if m.Step > 0 {
				stepLabel = fmt.Sprintf("[step %d] ", m.Step)
			}
			if _, err := fmt.Fprintf(c.OutOrStdout(), "%s[%s] %s\n%s\n\n", stepLabel, m.Role, m.Agent, m.Content); err != nil {
				return err
			}
		}
		if len(out) == 0 {
			_, err = fmt.Fprintln(c.OutOrStdout(), "No messages recorded.")
			return err
		}
		return nil
	}}
	cmd.Flags().Int64Var(&step, "step", -1, "show messages for one keyframe step")
	cmd.Flags().StringVar(&session, "session", "", "show messages for one session")
	cmd.Flags().IntVar(&limit, "limit", 0, "max messages to show (0 means all)")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip the first N matching messages")
	return cmd
}
