package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/attribution"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

type blameLine struct {
	Line         int                   `json:"line"`
	Agent        string                `json:"agent"`
	Step         int64                 `json:"step,omitempty"`
	Time         int64                 `json:"time,omitempty"`
	Commit       string                `json:"commit,omitempty"`
	OriginCommit string                `json:"origin_commit,omitempty"`
	Confidence   string                `json:"confidence"`
	RewriteChain []attribution.Rewrite `json:"rewrite_chain,omitempty"`
	Message      string                `json:"message,omitempty"`
}

type blameReport struct {
	File    string         `json:"file"`
	Lines   []blameLine    `json:"lines"`
	Summary map[string]int `json:"summary"`
}

func blameCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{Use: "blame <file>", Short: "Show who wrote each line of a file", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		file, err := attribution.ResolvePath(root, args[0])
		if err != nil {
			return err
		}
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		b, err := io.ReadAll(io.LimitReader(f, attribution.MaxTextBytes+1))
		if err := errors.Join(err, f.Close()); err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		if len(b) > attribution.MaxTextBytes {
			return fmt.Errorf("file is larger than %d bytes", attribution.MaxTextBytes)
		}
		current := string(b)
		var text []string
		if current != "" {
			text = strings.Split(strings.TrimSuffix(current, "\n"), "\n")
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		lines := make([]int, len(text))
		for i := range lines {
			lines[i] = i + 1
		}
		origins, err := attribution.ExplainLines(e.DB, e.Blobs, root, file, current, lines, false)
		if err != nil {
			return err
		}
		report := blameReport{File: file, Lines: make([]blameLine, 0, len(lines)), Summary: map[string]int{}}
		for i, line := range origins {
			origin := line[len(line)-1]
			report.Lines = append(report.Lines, blameLine{Line: i + 1, Agent: origin.Agent, Step: origin.Step, Time: origin.Time,
				Commit: origin.Commit, OriginCommit: origin.OriginCommit, Confidence: string(origin.Confidence),
				RewriteChain: origin.Rewrites, Message: origin.Message})
			report.Summary[origin.Agent]++
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(report)
		}
		for i, line := range report.Lines {
			label := line.Agent
			if line.Step > 0 {
				label = fmt.Sprintf("%s #%d", line.Agent, line.Step)
			}
			commit := "working"
			if line.Commit != "" {
				commit = line.Commit[:min(8, len(line.Commit))]
				if len(line.RewriteChain) > 0 {
					commit += "*"
				}
			}
			if _, err := fmt.Fprintf(c.OutOrStdout(), "%5d %-18s %-9s %-10s | %s\n", line.Line, label, commit, line.Confidence, text[i]); err != nil {
				return err
			}
		}
		return nil
	}}
}
