package cli

import (
	"encoding/json"
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

func dagCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "dag", Short: "Show the causal graph of edits (parent pointers)", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		type node struct {
			ID       string `json:"id"`
			Path     string `json:"path"`
			Kind     string `json:"kind"`
			Source   string `json:"source"`
			Parent   string `json:"parent,omitempty"`
			Children []string `json:"children,omitempty"`
		}
		rows, err := e.DB.Query("SELECT id,path,kind,source,COALESCE(parent_op_id,'') FROM ops ORDER BY at ASC")
		if err != nil {
			return err
		}
		defer rows.Close()
		nodes := map[string]*node{}
		var order []string
		for rows.Next() {
			var n node
			if err := rows.Scan(&n.ID, &n.Path, &n.Kind, &n.Source, &n.Parent); err != nil {
				return err
			}
			nodes[n.ID] = &n
			order = append(order, n.ID)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range order {
			if n := nodes[id]; n.Parent != "" {
				if p, ok := nodes[n.Parent]; ok {
					p.Children = append(p.Children, id)
				}
			}
		}
		var out []*node
		for _, id := range order {
			out = append(out, nodes[id])
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(out)
		}
		for _, n := range out {
			indent := ""
			if n.Parent != "" {
				indent = "  "
			}
			fmt.Fprintf(c.OutOrStdout(), "%s%s %s %s", indent, n.ID[:8], n.Source, n.Path)
			if n.Parent != "" {
				fmt.Fprintf(c.OutOrStdout(), " (parent %s)", n.Parent[:8])
			}
			fmt.Fprintln(c.OutOrStdout())
		}
		return nil
	}}
	return cmd
}