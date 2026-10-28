package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/restore"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

func (e *Engine) Context(step int64) (string, error) {
	var head string
	if err := e.DB.QueryRow("SELECT COALESCE(git_head,'') FROM keyframes WHERE seq=?", step).Scan(&head); err != nil {
		return "", fmt.Errorf("finding step: %w", err)
	}
	rows, err := e.DB.Query(`SELECT k.seq,COALESCE(t.prompt,''),COALESCE(t.reasoning,'') FROM keyframes k
		JOIN turns t ON t.id=k.turn_id WHERE k.seq<=? ORDER BY k.seq DESC LIMIT 5`, step)
	if err != nil {
		return "", fmt.Errorf("reading context turns: %w", err)
	}
	defer rows.Close()
	type turn struct {
		seq               int64
		prompt, reasoning string
	}
	var turns []turn
	for rows.Next() {
		var t turn
		if err := rows.Scan(&t.seq, &t.prompt, &t.reasoning); err != nil {
			return "", err
		}
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	var s strings.Builder
	fmt.Fprintf(&s, "# Trace context\n\nSource step: %d\nGit HEAD: %s\n\n## Recent turns\n", step, head)
	for i := len(turns) - 1; i >= 0; i-- {
		t := turns[i]
		fmt.Fprintf(&s, "\n### Step %d\n\nPrompt: %s\n", t.seq, t.prompt)
		if t.reasoning != "" {
			excerpt := t.reasoning
			if len(excerpt) > 400 {
				excerpt = textutil.Truncate(excerpt, 400)
			}
			fmt.Fprintf(&s, "Reasoning: %s\n", excerpt)
		}
	}
	target, err := e.ManifestAt(step)
	if err != nil {
		return "", err
	}
	previous := manifest.Manifest{}
	var prev int64
	if err := e.DB.QueryRow("SELECT seq FROM keyframes WHERE seq<? ORDER BY seq DESC LIMIT 1", step).Scan(&prev); err == nil {
		previous, err = e.ManifestAt(prev)
		if err != nil {
			return "", err
		}
	}
	files := restore.Plan(previous, target)
	fmt.Fprintf(&s, "\n## Files changed at step %d\n", step)
	for _, p := range files {
		fmt.Fprintf(&s, "- %s\n", p)
	}
	return s.String(), nil
}

func WriteContext(root, content string) error {
	dir := filepath.Join(root, ".trace")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating context directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "context.md"), []byte(content), 0600); err != nil {
		return fmt.Errorf("writing context: %w", err)
	}
	return nil
}
