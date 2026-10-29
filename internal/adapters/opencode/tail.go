package opencode

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// pollCursor retains fingerprints in an overlap window to tolerate updates
// committed out of timestamp order, including within the same millisecond.
type pollCursor struct {
	updated int64
	seen    map[string]seenPart
}

type seenPart struct {
	updated int64
	hash    [32]byte
}

const overlapMillis = int64(time.Minute / time.Millisecond)

func (c *pollCursor) cutoff() int64 {
	if c.updated <= overlapMillis {
		return 0
	}
	return c.updated - overlapMillis
}

func Run(ctx context.Context, root, dbPath string, process func(event.Event) error) error {
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolving home: %w", err)
		}
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		if !filepath.IsAbs(data) {
			return fmt.Errorf("XDG_DATA_HOME must be absolute")
		}
		dbPath = filepath.Join(data, "opencode", "opencode.db")
	}
	if !filepath.IsAbs(dbPath) {
		return fmt.Errorf("OpenCode database path must be absolute")
	}
	for {
		info, err := os.Stat(dbPath)
		if err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("OpenCode database is not a regular file")
			}
			break
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("checking OpenCode database: %w", err)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	uri := url.URL{Scheme: "file", Path: dbPath}
	query := uri.Query()
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return fmt.Errorf("opening OpenCode db: %w", err)
	}
	defer db.Close()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var cursor pollCursor
	// Database files can appear before OpenCode's migrations finish. Retry
	// initialization until the required schema is available or we stop.
	for {
		if err := initializeCursor(ctx, db, root, &cursor); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := poll(ctx, db, root, &cursor, process); err != nil {
				// keep running; log via returned? daemon logs
				_ = err
			}
		}
	}
}

func initializeCursor(ctx context.Context, db *sql.DB, root string, cursor *pollCursor) error {
	// Use a read transaction so the high-water mark and fingerprints describe
	// the same snapshot. Other repositories must not move this cursor forward.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("starting OpenCode cursor: %w", err)
	}
	defer tx.Rollback()
	var start pollCursor
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(p.time_updated),0) FROM part p JOIN session s ON s.id=p.session_id WHERE s.directory=?`, root).Scan(&start.updated); err != nil {
		return fmt.Errorf("finding latest OpenCode part: %w", err)
	}
	start.seen = make(map[string]seenPart)
	rows, err := tx.QueryContext(ctx, `SELECT p.id,p.time_updated,p.data FROM part p JOIN session s ON s.id=p.session_id WHERE s.directory=? AND p.time_updated>=?`, root, start.cutoff())
	if err != nil {
		return fmt.Errorf("reading OpenCode starting cursor: %w", err)
	}
	for rows.Next() {
		var id, data string
		var updated int64
		if err := rows.Scan(&id, &updated, &data); err != nil {
			rows.Close()
			return fmt.Errorf("reading OpenCode starting row: %w", err)
		}
		start.seen[id] = seenPart{updated, sha256.Sum256([]byte(data))}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("reading OpenCode starting rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("closing OpenCode starting rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("finishing OpenCode cursor: %w", err)
	}
	*cursor = start
	return nil
}

func poll(ctx context.Context, db *sql.DB, root string, cursor *pollCursor, process func(event.Event) error) error {
	rows, err := db.QueryContext(ctx, `SELECT p.id, p.session_id, p.time_updated, p.data, s.directory, COALESCE(json_extract(m.data,'$.role'),'')
        FROM part p JOIN session s ON s.id=p.session_id LEFT JOIN message m ON m.id=p.message_id
        WHERE p.time_updated >= ? AND s.directory = ? ORDER BY p.time_updated, p.id`, cursor.cutoff(), root)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		id, sessionID, data, dir, role string
		timeCreated                    int64
	}
	var collected []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.sessionID, &r.timeCreated, &r.data, &r.dir, &r.role); err != nil {
			return fmt.Errorf("reading OpenCode part: %w", err)
		}
		collected = append(collected, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("reading OpenCode parts: %w", err)
	}
	rows.Close()
	for _, r := range collected {
		fingerprint := sha256.Sum256([]byte(r.data))
		if previous, ok := cursor.seen[r.id]; ok && previous.hash == fingerprint && previous.updated == r.timeCreated {
			continue
		}
		if cursor.seen == nil {
			cursor.seen = make(map[string]seenPart)
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(r.data), &d); err != nil {
			cursor.seen[r.id] = seenPart{r.timeCreated, fingerprint}
			if r.timeCreated > cursor.updated {
				cursor.updated = r.timeCreated
			}
			continue
		}
		typ, _ := d["type"].(string)
		switch typ {
		case "text":
			// user prompt? only emit if the message role is user
			if r.role == "user" {
				text, _ := d["text"].(string)
				if text != "" {
					ev := event.Event{Kind: event.TurnStart, Agent: "opencode", AgentSessionID: r.sessionID, Worktree: root, At: r.timeCreated, Prompt: text}
					if err := process(ev); err != nil {
						return err
					}
				}
			}
		case "tool":
			tool, _ := d["tool"].(string)
			state, _ := d["state"].(map[string]any)
			status, _ := state["status"].(string)
			if status != "completed" {
				break
			}
			input, _ := state["input"].(map[string]any)
			path, _ := input["filePath"].(string)
			if tool == "edit" || tool == "write" {
				if path != "" {
					ev := event.Event{Kind: event.ToolEdit, Agent: "opencode", AgentSessionID: r.sessionID, Worktree: root, At: r.timeCreated, ToolName: tool, Paths: []string{path}}
					if err := process(ev); err != nil {
						return err
					}
				}
			} else if tool == "bash" {
				ev := event.Event{Kind: event.ToolEdit, Agent: "opencode", AgentSessionID: r.sessionID, Worktree: root, At: r.timeCreated, ToolName: tool}
				if err := process(ev); err != nil {
					return err
				}
			}
		case "step-finish":
			reason, _ := d["reason"].(string)
			if reason != "stop" {
				break
			}
			ev := event.Event{Kind: event.TurnEnd, Agent: "opencode", AgentSessionID: r.sessionID, Worktree: root, At: r.timeCreated}
			if err := process(ev); err != nil {
				return err
			}
		case "reasoning":
			text, _ := d["text"].(string)
			partTime, _ := d["time"].(map[string]any)
			if text != "" && partTime["end"] != nil {
				ev := event.Event{Kind: event.TurnContext, Agent: "opencode", AgentSessionID: r.sessionID, Worktree: root, At: r.timeCreated, Text: text, ContextType: "thinking"}
				if err := process(ev); err != nil {
					return err
				}
			}
		}
		cursor.seen[r.id] = seenPart{r.timeCreated, fingerprint}
		if r.timeCreated > cursor.updated {
			cursor.updated = r.timeCreated
		}
	}
	for id, seen := range cursor.seen {
		if seen.updated < cursor.cutoff() {
			delete(cursor.seen, id)
		}
	}
	return nil
}
