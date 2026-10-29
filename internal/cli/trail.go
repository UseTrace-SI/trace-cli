package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/id"
	"github.com/spf13/cobra"
)

func trailCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "trail", Short: "Manage named trails over recorded history"}
	create := &cobra.Command{Use: "create", Short: "Create a trail from the current latest step", RunE: func(c *cobra.Command, _ []string) error {
		title, _ := c.Flags().GetString("title")
		kind, _ := c.Flags().GetString("type")
		if title == "" {
			return fmt.Errorf("title is required")
		}
		if kind == "" {
			kind = "feature"
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
		var base, head int64
		if err := e.DB.QueryRow("SELECT COALESCE(MIN(seq),0),COALESCE(MAX(seq),0) FROM keyframes").Scan(&base, &head); err != nil {
			return err
		}
		var count int64
		if err := e.DB.QueryRow("SELECT COUNT(*) FROM keyframes").Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("no recorded history to trail")
		}
		trailID, err := id.New()
		if err != nil { return err }
		if _, err := e.DB.Exec("INSERT INTO trails(id,title,kind,base_step,head_step,created_at) VALUES(?,?,?,?,?,?)",
			trailID, title, kind, base, head, time.Now().UnixMilli()); err != nil {
			return fmt.Errorf("creating trail: %w", err)
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"id": trailID, "title": title, "kind": kind, "base_step": base, "head_step": head})
		}
		if _, err := fmt.Fprintf(c.OutOrStdout(), "Created trail %s: %s (%s) steps %d..%d\n", trailID, title, kind, base, head); err != nil {
			return err
		}
		return nil
	}}
	create.Flags().String("title", "", "trail title")
	create.Flags().String("type", "feature", "trail type")
	list := &cobra.Command{Use: "list", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		rows, err := e.DB.Query("SELECT id,title,kind,base_step,head_step,created_at FROM trails ORDER BY created_at DESC")
		if err != nil {
			return err
		}
		defer rows.Close()
		type trail struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Kind      string `json:"kind"`
			BaseStep  int64  `json:"base_step"`
			HeadStep  int64  `json:"head_step"`
			CreatedAt int64  `json:"created_at"`
		}
		var trails []trail
		for rows.Next() {
			var t trail
			if err := rows.Scan(&t.ID, &t.Title, &t.Kind, &t.BaseStep, &t.HeadStep, &t.CreatedAt); err != nil {
				return err
			}
			trails = append(trails, t)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(trails)
		}
		if len(trails) == 0 {
			_, err = fmt.Fprintln(c.OutOrStdout(), "No trails.")
			return err
		}
		for _, t := range trails {
			if _, err := fmt.Fprintf(c.OutOrStdout(), "%s  %-24s  %s  steps %d..%d\n", t.ID, t.Title, t.Kind, t.BaseStep, t.HeadStep); err != nil {
				return err
			}
		}
		return nil
	}}
	show := &cobra.Command{Use: "show <id>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		var title, kind string
		var base, head, created int64
		if err := e.DB.QueryRow("SELECT title,kind,base_step,head_step,created_at FROM trails WHERE id=?", args[0]).Scan(&title, &kind, &base, &head, &created); err != nil {
			return fmt.Errorf("trail not found: %w", err)
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"id": args[0], "title": title, "kind": kind, "base_step": base, "head_step": head, "created_at": created})
		}
		if _, err := fmt.Fprintf(c.OutOrStdout(), "Trail %s: %s (%s)\nSteps %d..%d\nCreated %d\n", args[0], title, kind, base, head, created); err != nil {
			return err
		}
		return nil
	}}
	cmd.AddCommand(create, list, show)
	return cmd
}
