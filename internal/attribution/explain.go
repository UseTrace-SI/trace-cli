// Package attribution explains which recorded edit produced a line of a file.
// It is shared by `trace why` and the read-only MCP server so both apply the
// same alignment between the current file and recorded history.
package attribution

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
	"github.com/UseTrace-SI/trace-cli/internal/why"
)

// MaxTextBytes bounds every file state read while explaining a line.
const MaxTextBytes = 1 << 20

// Origin describes one recorded edit, or the reason no edit can be named.
type Origin struct {
	Step      int64
	Agent     string
	Time      int64
	Prompt    string
	Reasoning string
	Commit    string
	Symbol    string
	Message   string
}

// Querier is the database surface used to read history.
type Querier interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

// ResolvePath validates a user-supplied path and returns it relative to root.
// Paths outside the repository and secret or excluded paths are rejected so
// that their contents are never read.
func ResolvePath(root, path string) (string, error) {
	file, err := repo.RelativePath(root, path)
	if err != nil || manifest.Excluded(file) {
		return "", errors.New("unsafe file path")
	}
	return file, nil
}

// ExplainFile reads the current working copy of file (relative to root) and
// explains the given 1-based line.
func ExplainFile(db Querier, blobs *blob.Store, root, file string, line int, all bool) ([]Origin, error) {
	if line < 1 {
		return nil, errors.New("invalid line number")
	}
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxTextBytes+1))
	closeErr := f.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	if len(b) > MaxTextBytes {
		return []Origin{{Agent: "unknown", Message: "origin uncertain after a large edit"}}, nil
	}
	current := string(b)
	if line > len(strings.Split(strings.TrimSuffix(current, "\n"), "\n")) {
		return nil, errors.New("line is outside the file")
	}
	return Explain(db, blobs, file, current, line, all)
}

// Explain maps a line of current, the working text of file, back through the
// recorded file states. The working file may have changed since the latest
// capture, including during a Git rewrite, so it is aligned with each recorded
// state before stored line ranges are used; an uncaptured line is never
// attributed to an agent.
func Explain(db Querier, blobs *blob.Store, file, current string, line int, all bool) ([]Origin, error) {
	rows, err := db.Query(`SELECT before_hash,after_hash,COALESCE(turn_id,''),at,COALESCE(detail,''),COALESCE(start_line,0),COALESCE(end_line,0) FROM ops
		WHERE path=? AND (before_hash IS NOT NULL OR after_hash IS NOT NULL) ORDER BY at DESC,id DESC`, file)
	if err != nil {
		return nil, fmt.Errorf("querying file history: %w", err)
	}
	type change struct {
		before, after, turnID, detail string
		at                            int64
		start, end                    int
	}
	var changes []change
	for rows.Next() {
		var ch change
		var before, after sql.NullString
		if err := rows.Scan(&before, &after, &ch.turnID, &ch.at, &ch.detail, &ch.start, &ch.end); err != nil {
			rows.Close()
			return nil, err
		}
		ch.before, ch.after = before.String, after.String
		changes = append(changes, ch)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var results []Origin
	uncertain := false
	uncertaintyMessage := "origin uncertain after a large edit"
	recordedText := current
	type cachedText struct {
		text      string
		truncated bool
	}
	textCache := map[string]cachedText{"": {}}
	loadText := func(hash string) (cachedText, error) {
		if value, ok := textCache[hash]; ok {
			return value, nil
		}
		text, truncated, err := textLimited(blobs, blob.Hash(hash))
		if err != nil {
			return cachedText{}, err
		}
		value := cachedText{text: text, truncated: truncated}
		if len(textCache) >= 64 {
			textCache = map[string]cachedText{"": {}}
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
		oldText, err := loadText(ch.before)
		if err != nil {
			return nil, err
		}
		newText, err := loadText(ch.after)
		if err != nil {
			return nil, err
		}
		if oldText.truncated || newText.truncated {
			uncertain = true
			break
		}
		if recordedText != newText.text {
			mapped, touched := why.MapLine(newText.text, recordedText, line)
			if touched || mapped < 1 {
				uncertain = true
				uncertaintyMessage = "origin uncertain: line cannot be matched to the recorded history"
				break
			}
			line = mapped
		}
		recordedText = oldText.text
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
		turnID, at := ch.turnID, ch.at
		oldLine, touched := why.MapLine(oldText.text, newText.text, line)
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
			r := Origin{Time: at, Symbol: symbol}
			if turnID == "" {
				r.Agent = "human"
				r.Message = "edited by you"
			} else {
				err = db.QueryRow(`SELECT COALESCE(k.seq,0),s.agent,COALESCE(t.prompt,''),COALESCE(t.reasoning,''),COALESCE(c.sha,'')
					FROM turns t JOIN sessions s ON s.id=t.session_id LEFT JOIN keyframes k ON k.turn_id=t.id
					LEFT JOIN commits c ON c.keyframe_id=k.id WHERE t.id=?`, turnID).Scan(&r.Step, &r.Agent, &r.Prompt, &r.Reasoning, &r.Commit)
				if err == sql.ErrNoRows {
					r.Message = "turn has no keyframe yet"
				} else if err != nil {
					return nil, err
				}
				if len(r.Reasoning) > 400 {
					r.Reasoning = textutil.Truncate(r.Reasoning, 400)
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
		results = append(results, Origin{Agent: "unknown", Message: uncertaintyMessage})
	} else if len(results) == 0 {
		results = append(results, Origin{Agent: "baseline", Message: "present at baseline"})
	}
	return results, nil
}

func textLimited(bs *blob.Store, h blob.Hash) (string, bool, error) {
	if h == "" {
		return "", false, nil
	}
	r, err := bs.Get(h)
	if err != nil {
		return "", false, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, MaxTextBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(b) > MaxTextBytes {
		return textutil.TruncateBytes(string(b), MaxTextBytes), true, nil
	}
	return string(b), false, nil
}
