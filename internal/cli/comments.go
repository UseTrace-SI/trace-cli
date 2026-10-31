package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func commentsCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "comments", Short: "Manage anchored review comments"}
	add := &cobra.Command{Use: "add <file> <line> <body...>", Args: cobra.MinimumNArgs(3), RunE: func(c *cobra.Command, args []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		id := fmt.Sprintf("cmt-%d", nowMillis())
		body := ""
		for i := 2; i < len(args); i++ {
			if i > 2 {
				body += " "
			}
			body += args[i]
		}
		f := args[0]
		ln, err := atoi(args[1])
		if err != nil {
			return err
		}
		if _, err := e.DB.Exec("INSERT INTO comments(id,file,line,body,at) VALUES(?,?,?,?,?)", id, f, ln, body, nowMillis()); err != nil {
			return err
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "added comment %s on %s:%d\n", id, f, ln)
		return err
	}}
	list := &cobra.Command{Use: "list [file]", Args: cobra.MaximumNArgs(1), RunE: func(c *cobra.Command, args []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		q := "SELECT id,file,line,body,at FROM comments"
		var a []any
		if len(args) == 1 {
			q += " WHERE file=?"
			a = append(a, args[0])
		}
		q += " ORDER BY at DESC"
		rows, err := e.DB.Query(q, a...)
		if err != nil {
			return err
		}
		defer rows.Close()
		type cmt struct {
			ID   string `json:"id"`
			File string `json:"file"`
			Line int    `json:"line"`
			Body string `json:"body"`
			At   int64  `json:"at"`
		}
		var out []cmt
		for rows.Next() {
			var cm cmt
			rows.Scan(&cm.ID, &cm.File, &cm.Line, &cm.Body, &cm.At)
			out = append(out, cm)
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(out)
		}
		for _, cm := range out {
			fmt.Fprintf(c.OutOrStdout(), "%s %s:%d %s\n", cm.ID, cm.File, cm.Line, cm.Body)
		}
		return nil
	}}
	cmd.AddCommand(add, list)
	return cmd
}

func nowMillis() int64 {
	return time.Now().UnixMilli()
}

func atoi(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}