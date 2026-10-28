package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/UseTrace-SI/trace-cli/internal/why"
	"github.com/spf13/cobra"
)

type whyResult struct {
	Step      int64  `json:"step,omitempty"`
	Agent     string `json:"agent"`
	Time      int64  `json:"time"`
	Prompt    string `json:"prompt,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	Commit    string `json:"commit,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
	Message   string `json:"message,omitempty"`
}

func whyCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var all bool
	cmd := &cobra.Command{Use: "why <file>:<line>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		pos := strings.LastIndex(args[0], ":")
		if pos < 0 {
			return fmt.Errorf("expected file:line")
		}
		line, err := strconv.Atoi(args[0][pos+1:])
		if err != nil || line < 1 {
			return fmt.Errorf("invalid line number")
		}
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		file, err := repo.RelativePath(root, args[0][:pos])
		if err != nil || manifest.Excluded(file) {
			return fmt.Errorf("unsafe file path")
		}
		current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		if line > len(strings.Split(strings.TrimSuffix(string(current), "\n"), "\n")) {
			return fmt.Errorf("line is outside the file")
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		rows, err := e.DB.Query(`SELECT before_hash,after_hash,COALESCE(turn_id,''),at,COALESCE(detail,''),COALESCE(start_line,0),COALESCE(end_line,0) FROM ops
			WHERE path=? AND (before_hash IS NOT NULL OR after_hash IS NOT NULL) ORDER BY at DESC,id DESC`, file)
		if err != nil {
			return fmt.Errorf("querying file history: %w", err)
		}
		type change struct {
			before, after, turnID, detail string
			at                            int64
			start, end                    int
		}
		var changes []change
		for rows.Next() {
			var ch change
			if err := rows.Scan(&ch.before, &ch.after, &ch.turnID, &ch.at, &ch.detail, &ch.start, &ch.end); err != nil {
				rows.Close()
				return err
			}
			changes = append(changes, ch)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		var results []whyResult
		uncertain := false
		textCache := map[string]string{"": ""}
		loadText := func(hash string) (string, error) {
			if value, ok := textCache[hash]; ok {
				return value, nil
			}
			value, err := blobText(e.Blobs, blob.Hash(hash))
			if err != nil {
				return "", err
			}
			if len(textCache) >= 64 {
				textCache = map[string]string{"": ""}
			}
			textCache[hash] = value
			return value, nil
		}
		for i := 0; i < len(changes); {
			ch := changes[i]
			j := i + 1
			for j < len(changes) && changes[j].before == ch.before && changes[j].after == ch.after && changes[j].turnID == ch.turnID {
				j++
			}
			group := changes[i:j]
			i = j
			laterInFile := true
			for _, candidate := range group {
				if candidate.start == 0 || candidate.start <= line {
					laterInFile = false
					break
				}
			}
			if laterInFile {
				continue
			}
			before, after, turnID, at := ch.before, ch.after, ch.turnID, ch.at
			oldText, err := loadText(before)
			if err != nil {
				return err
			}
			newText, err := loadText(after)
			if err != nil {
				return err
			}
			oldLine, touched := why.MapLine(oldText, newText, line)
			if oldLine < 0 {
				uncertain = true
				break
			}
			if touched {
				symbol := ""
				for _, candidate := range group {
					if candidate.detail != "" && line >= candidate.start && line <= candidate.end {
						symbol = candidate.detail
						break
					}
				}
				r := whyResult{Time: at, Symbol: symbol}
				if turnID == "" {
					r.Agent = "human"
					r.Message = "edited by you"
				} else {
					err = e.DB.QueryRow(`SELECT COALESCE(k.seq,0),s.agent,COALESCE(t.prompt,''),COALESCE(t.reasoning,''),COALESCE(c.sha,'')
						FROM turns t JOIN sessions s ON s.id=t.session_id LEFT JOIN keyframes k ON k.turn_id=t.id
						LEFT JOIN commits c ON c.keyframe_id=k.id WHERE t.id=?`, turnID).Scan(&r.Step, &r.Agent, &r.Prompt, &r.Reasoning, &r.Commit)
					if err == sql.ErrNoRows {
						r.Message = "turn has no keyframe yet"
					} else if err != nil {
						return err
					}
					if len(r.Reasoning) > 400 {
						r.Reasoning = r.Reasoning[:400]
					}
				}
				results = append(results, r)
				if !all {
					break
				}
			}
			line = oldLine
			if line < 1 {
				break
			}
		}
		if uncertain {
			results = append(results, whyResult{Agent: "unknown", Message: "origin uncertain after a large edit"})
		} else if len(results) == 0 {
			results = append(results, whyResult{Agent: "baseline", Message: "present at baseline"})
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(results)
		}
		for _, r := range results {
			if r.Symbol != "" {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "Symbol: %s\n", r.Symbol); err != nil {
					return err
				}
			}
			if r.Step > 0 {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "Step %d: %s — %s\n", r.Step, r.Agent, r.Prompt); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintln(c.OutOrStdout(), r.Message); err != nil {
					return err
				}
			}
		}
		return nil
	}}
	cmd.Flags().BoolVar(&all, "all", false, "show all matching edits")
	return cmd
}
