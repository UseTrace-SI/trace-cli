package store

import (
	"database/sql"
	"fmt"
)

func migrate(db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY);
INSERT OR IGNORE INTO schema_version(version) VALUES (1);
CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, agent TEXT NOT NULL, agent_sid TEXT,
  worktree TEXT NOT NULL, started_at INTEGER NOT NULL, ended_at INTEGER, forked_from TEXT);
CREATE TABLE IF NOT EXISTS turns (id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id),
  seq INTEGER NOT NULL UNIQUE, prompt TEXT, reasoning TEXT, tool_calls TEXT, files_read TEXT,
  started_at INTEGER NOT NULL, ended_at INTEGER);
CREATE TABLE IF NOT EXISTS keyframes (id TEXT PRIMARY KEY, turn_id TEXT REFERENCES turns(id),
  seq INTEGER NOT NULL UNIQUE, manifest_hash TEXT NOT NULL, git_head TEXT,
  tag TEXT, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS ops (id TEXT PRIMARY KEY, turn_id TEXT REFERENCES turns(id), session_id TEXT,
  path TEXT NOT NULL, kind TEXT NOT NULL, start_line INTEGER, end_line INTEGER,
  before_hash TEXT, after_hash TEXT, source TEXT NOT NULL, at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS ops_path ON ops(path, at);
CREATE TABLE IF NOT EXISTS commits (sha TEXT PRIMARY KEY, keyframe_id TEXT REFERENCES keyframes(id),
  at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS commits_at_sha ON commits(at, sha);
`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("migrating database: %w", err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=3").Scan(&n); err != nil {
		return fmt.Errorf("checking search migration: %w", err)
	}
	if n == 0 {
		const fts = `
CREATE VIRTUAL TABLE IF NOT EXISTS turn_fts USING fts5(prompt, reasoning, content='turns', content_rowid='rowid');
CREATE TRIGGER IF NOT EXISTS turns_ai AFTER INSERT ON turns BEGIN
  INSERT INTO turn_fts(rowid,prompt,reasoning) VALUES(new.rowid,new.prompt,new.reasoning);
END;
CREATE TRIGGER IF NOT EXISTS turns_ad AFTER DELETE ON turns BEGIN
  INSERT INTO turn_fts(turn_fts,rowid,prompt,reasoning) VALUES('delete',old.rowid,old.prompt,old.reasoning);
END;
CREATE TRIGGER IF NOT EXISTS turns_au AFTER UPDATE ON turns BEGIN
  INSERT INTO turn_fts(turn_fts,rowid,prompt,reasoning) VALUES('delete',old.rowid,old.prompt,old.reasoning);
  INSERT INTO turn_fts(rowid,prompt,reasoning) VALUES(new.rowid,new.prompt,new.reasoning);
END;`
		if _, err := db.Exec(fts); err != nil {
			return fmt.Errorf("creating history search: %w", err)
		}
		if _, err := db.Exec("INSERT INTO turn_fts(turn_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("indexing history: %w", err)
		}
		if _, err := db.Exec("INSERT INTO schema_version(version) VALUES(3)"); err != nil {
			return fmt.Errorf("recording search migration: %w", err)
		}
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=4").Scan(&n); err != nil {
		return fmt.Errorf("checking structural migration: %w", err)
	}
	if n == 0 {
		if _, err := db.Exec("ALTER TABLE ops ADD COLUMN detail TEXT"); err != nil {
			return fmt.Errorf("adding structural detail: %w", err)
		}
		if _, err := db.Exec("INSERT INTO schema_version(version) VALUES(4)"); err != nil {
			return fmt.Errorf("recording structural migration: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS transcript_offsets (
  session_id TEXT PRIMARY KEY REFERENCES sessions(id),
  path TEXT NOT NULL,
  offset INTEGER NOT NULL
)`); err != nil {
		return fmt.Errorf("creating transcript offsets: %w", err)
	}
	if _, err := db.Exec("INSERT OR IGNORE INTO schema_version(version) VALUES(5)"); err != nil {
		return fmt.Errorf("recording transcript migration: %w", err)
	}
	return nil
}
