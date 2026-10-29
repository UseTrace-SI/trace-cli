package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/historysearch"
	"github.com/spf13/cobra"
)

func searchCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var limit int
	var scope, editedPath, rank string
	var fuzzy bool
	var allRepos bool
	cmd := &cobra.Command{Use: "search [terms...]", RunE: func(c *cobra.Command, args []string) error {
		var roots []string
		if allRepos {
			filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
				if err != nil || !info.IsDir() {
					return nil
				}
				if info.Name() == ".trace" {
					roots = append(roots, filepath.Dir(path))
					return filepath.SkipDir
				}
				return nil
			})
			if len(roots) == 0 {
				roots = append(roots, ".")
			}
		} else {
			root, err := initializedRoot(*repoPath)
			if err != nil {
				return err
			}
			roots = append(roots, root)
		}
		var allHits []historysearch.Hit
		for _, r := range roots {
			e, err := engine.Open(r)
			if err != nil {
				continue
			}
			hits, err := historysearch.Search(e.DB.DB, historysearch.Options{
				Query: strings.Join(args, " "), Scope: scope, EditedPath: editedPath, Limit: limit, Fuzzy: fuzzy, Rank: rank,
			})
			e.Close()
			if err != nil {
				continue
			}
			for i := range hits {
				hits[i].Repo = r
			}
			allHits = append(allHits, hits...)
		}
		hits := allHits
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(hits)
		}
		if len(hits) == 0 {
			_, err := fmt.Fprintln(c.OutOrStdout(), "No matches.")
			return err
		}
		for _, h := range hits {
			line := fmt.Sprintf("%d  %-12s  %s", h.Step, h.Agent, h.Prompt)
			if h.Repo != "" {
				line = h.Repo + "  " + line
			}
			if _, err := fmt.Fprintln(c.OutOrStdout(), line); err != nil {
				return err
			}
			if h.Snippet != "" {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "     %s\n", h.Snippet); err != nil {
					return err
				}
			}
		}
		return nil
	}}
	cmd.Flags().IntVar(&limit, "limit", 20, "max results")
	cmd.Flags().StringVar(&scope, "scope", "all", "search all turn text or only turns or messages")
	cmd.Flags().StringVar(&editedPath, "edited-path", "", "only turns that edited a matching path (glob)")
	cmd.Flags().BoolVar(&fuzzy, "fuzzy", false, "match terms by prefix for typo-tolerant search")
	cmd.Flags().BoolVar(&allRepos, "all-repos", false, "search every repository under the current directory")
	cmd.Flags().StringVar(&rank, "rank", "recency", "rank results by recency or relevance")
	return cmd
}
