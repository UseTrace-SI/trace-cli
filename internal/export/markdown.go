package export

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/attribution"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

const (
	maxPromptRunes   = 4000
	maxResponseRunes = 1500
)

// Sessions lists recorded session IDs, oldest first.
func Sessions(db attribution.Querier) ([]string, error) {
	rows, err := db.Query("SELECT id FROM sessions ORDER BY started_at,id")
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, id)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}

func stamp(ms int64) string {
	if ms <= 0 {
		return "—"
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func quote(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return "> _(none recorded)_\n"
	}
	return "> " + strings.ReplaceAll(s, "\n", "\n> ") + "\n"
}

type mdTurn struct {
	id             string
	step           int64
	prompt         string
	started, ended int64
	response       string
	files          map[string][]string
}

// SessionMarkdown renders one session: each turn's prompt, a summary of the
// last assistant response and the files its recorded edits touched. Text is
// passed through the redactor again and secret or .traceignore'd paths are
// left out.
func SessionMarkdown(db attribution.Querier, root, sessionID string) (string, error) {
	var agent string
	var started, ended int64
	if err := db.QueryRow("SELECT agent,started_at,COALESCE(ended_at,0) FROM sessions WHERE id=?", sessionID).Scan(&agent, &started, &ended); err != nil {
		return "", fmt.Errorf("reading session %s: %w", sessionID, err)
	}
	rows, err := db.Query(`SELECT t.id,COALESCE(k.seq,0),COALESCE(t.prompt,''),t.started_at,COALESCE(t.ended_at,0)
		FROM turns t LEFT JOIN keyframes k ON k.turn_id=t.id WHERE t.session_id=? ORDER BY t.seq`, sessionID)
	if err != nil {
		return "", fmt.Errorf("reading turns: %w", err)
	}
	var turns []*mdTurn
	for rows.Next() {
		t := &mdTurn{files: map[string][]string{}}
		if err := rows.Scan(&t.id, &t.step, &t.prompt, &t.started, &t.ended); err != nil {
			rows.Close()
			return "", err
		}
		turns = append(turns, t)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", err
	}
	for _, t := range turns {
		err := db.QueryRow(`SELECT COALESCE(content,'') FROM messages WHERE turn_id=? AND role='assistant'
			AND COALESCE(content,'')!='' ORDER BY at DESC,id DESC LIMIT 1`, t.id).Scan(&t.response)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("reading messages: %w", err)
		}
		ops, err := db.Query("SELECT path,kind FROM ops WHERE turn_id=? AND path!='' ORDER BY at,id", t.id)
		if err != nil {
			return "", fmt.Errorf("reading edits: %w", err)
		}
		for ops.Next() {
			var path, kind string
			if err := ops.Scan(&path, &kind); err != nil {
				ops.Close()
				return "", err
			}
			if !exportable(root, path) {
				continue
			}
			kinds := t.files[path]
			if len(kinds) == 0 || kinds[len(kinds)-1] != kind {
				t.files[path] = append(kinds, kind)
			}
		}
		if err := errors.Join(ops.Err(), ops.Close()); err != nil {
			return "", err
		}
	}
	red := redact.Redactor{}
	var b strings.Builder
	fmt.Fprintf(&b, "# Trace session %s\n\n- Agent: %s\n- Started: %s\n- Ended: %s\n- Turns: %d\n", sessionID, agent, stamp(started), stamp(ended), len(turns))
	for i, t := range turns {
		fmt.Fprintf(&b, "\n## Turn %d", i+1)
		if t.step > 0 {
			fmt.Fprintf(&b, " (step %d)", t.step)
		}
		fmt.Fprintf(&b, "\n\nTurn `%s`, %s to %s\n\n### Prompt\n\n%s", t.id, stamp(t.started), stamp(t.ended), quote(textutil.Truncate(red.Text(t.prompt), maxPromptRunes)))
		fmt.Fprintf(&b, "\n### Response summary\n\n%s", quote(textutil.Truncate(red.Text(t.response), maxResponseRunes)))
		b.WriteString("\n### Files touched\n\n")
		if len(t.files) == 0 {
			b.WriteString("_(none recorded)_\n")
			continue
		}
		paths := make([]string, 0, len(t.files))
		for p := range t.files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			fmt.Fprintf(&b, "- `%s` (%s)\n", strings.ReplaceAll(p, "`", "'"), strings.Join(t.files[p], ", "))
		}
	}
	return b.String(), nil
}
