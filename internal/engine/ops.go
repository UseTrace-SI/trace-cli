package engine

import (
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/id"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/structure"
)

type hunk struct{ start, end int }

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func changedHunks(before, after string) []hunk {
	a, b := lines(before), lines(after)
	n, m := len(a), len(b)
	if n > 2000 || m > 2000 {
		return []hunk{{1, m}}
	}
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []hunk
	for i, j := 0, 0; i < n || j < m; {
		if i < n && j < m && a[i] == b[j] {
			i++
			j++
			continue
		}
		start := j
		for i < n || j < m {
			if i < n && j < m && a[i] == b[j] {
				break
			}
			if i < n && (j == m || dp[i+1][j] >= dp[i][j+1]) {
				i++
			} else {
				j++
			}
		}
		if j > start {
			out = append(out, hunk{start + 1, j})
		} else {
			out = append(out, hunk{0, 0})
		}
	}
	return out
}

func (e *Engine) blobText(h blob.Hash) (string, error) {
	if h == "" {
		return "", nil
	}
	r, err := e.Blobs.Get(h)
	if err != nil {
		return "", err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return "", fmt.Errorf("reading changed blob: %w", err)
	}
	return string(b), nil
}

func (e *Engine) RecordChanges(dirty []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.recordChanges(dirty, false)
	return err
}

func (e *Engine) recordChanges(dirty []string, full bool) (manifest.Manifest, error) {
	var next manifest.Manifest
	var err error
	if full || e.current == nil {
		next, err = manifest.Scan(e.Root, nil, e.Blobs)
	} else {
		next, err = manifest.Update(e.current, e.Root, dirty, nil, e.Blobs)
	}
	if err != nil {
		return nil, err
	}
	return e.recordManifest(next)
}

func (e *Engine) recordManifest(next manifest.Manifest) (manifest.Manifest, error) {
	if e.current == nil {
		e.current = next
		return next, nil
	}
	changed := make([]string, 0)
	added, modified, deleted := manifest.Diff(e.current, next)
	changed = append(changed, added...)
	changed = append(changed, modified...)
	changed = append(changed, deleted...)
	if len(changed) == 0 {
		e.current = next
		return next, nil
	}
	var turnID, sessionID string
	err := e.DB.QueryRow(`SELECT t.id,t.session_id FROM turns t JOIN sessions s ON s.id=t.session_id
		WHERE s.worktree=? AND t.ended_at IS NULL ORDER BY t.started_at DESC LIMIT 1`, e.Root).Scan(&turnID, &sessionID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("finding active turn: %w", err)
	}
	if err == sql.ErrNoRows {
		turnID, sessionID = "", ""
	}
	tx, err := e.DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("starting change record: %w", err)
	}
	defer tx.Rollback()
	for _, p := range changed {
		oldHash := e.current[p].Hash
		newHash := next[p].Hash
		before, err := e.blobText(oldHash)
		if err != nil {
			return nil, err
		}
		after, err := e.blobText(newHash)
		if err != nil {
			return nil, err
		}
		kind := "modify"
		if oldHash == "" {
			kind = "add"
		} else if newHash == "" {
			kind = "delete"
		}
		opAt := time.Now().UnixMilli()
		if turnID != "" {
			var count int
			err := tx.QueryRow(`SELECT COUNT(*) FROM ops WHERE path=? AND session_id IS NOT NULL
				AND session_id<>? AND at>=? AND at<=?`, p, sessionID, opAt-2000, opAt).Scan(&count)
			if err != nil {
				return nil, fmt.Errorf("checking concurrent edits: %w", err)
			}
			if count > 0 {
				kind = "contested"
			}
		}
		source := "watcher"
		if turnID != "" {
			var n int
			if err := tx.QueryRow("SELECT COUNT(*) FROM ops WHERE turn_id=? AND path=? AND source='tool'", turnID, p).Scan(&n); err != nil {
				return nil, fmt.Errorf("checking tool edit: %w", err)
			}
			if n > 0 {
				source = "tool"
			}
		}
		for _, h := range changedHunks(before, after) {
			opID, err := id.New()
			if err != nil {
				return nil, err
			}
			var tid, sid any
			if turnID != "" {
				tid = turnID
				sid = sessionID
			}
			_, err = tx.Exec("INSERT INTO ops(id,turn_id,session_id,path,kind,start_line,end_line,before_hash,after_hash,source,at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", opID, tid, sid, p, kind, h.start, h.end, oldHash, newHash, source, opAt)
			if err != nil {
				return nil, fmt.Errorf("recording file change: %w", err)
			}
		}
		for _, symbol := range structure.Classify(p, before, after) {
			opID, err := id.New()
			if err != nil {
				return nil, err
			}
			var tid, sid any
			if turnID != "" {
				tid, sid = turnID, sessionID
			}
			_, err = tx.Exec("INSERT INTO ops(id,turn_id,session_id,path,kind,detail,start_line,end_line,before_hash,after_hash,source,at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
				opID, tid, sid, p, symbol.Kind, symbol.Name, symbol.StartLine, symbol.EndLine, oldHash, newHash, source, time.Now().UnixMilli())
			if err != nil {
				return nil, fmt.Errorf("recording structural change: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing change record: %w", err)
	}
	e.current = next
	return next, nil
}
