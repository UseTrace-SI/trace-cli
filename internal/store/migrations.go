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
CREATE INDEX IF NOT EXISTS ops_turn_path ON ops(turn_id, path);
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
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS sync_updates (
  id TEXT PRIMARY KEY,
  table_name TEXT NOT NULL,
  row_id TEXT NOT NULL,
  data TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("creating sync updates: %w", err)
	}
	if _, err := db.Exec("INSERT OR IGNORE INTO schema_version(version) VALUES(6)"); err != nil {
		return fmt.Errorf("recording sync update migration: %w", err)
	}
	var n7 int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=7").Scan(&n7); err != nil {
		return fmt.Errorf("checking actor migration: %w", err)
	}
	if n7 == 0 {
		if _, err := db.Exec("ALTER TABLE ops ADD COLUMN actor TEXT"); err != nil {
			return fmt.Errorf("adding actor column: %w", err)
		}
		if _, err := db.Exec("INSERT INTO schema_version(version) VALUES(7)"); err != nil {
			return fmt.Errorf("recording actor migration: %w", err)
		}
	}
	var n8 int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=8").Scan(&n8); err != nil {
		return fmt.Errorf("checking messages migration: %w", err)
	}
	if n8 == 0 {
		const msgs = `
CREATE TABLE IF NOT EXISTS messages (id TEXT PRIMARY KEY, session_id TEXT REFERENCES sessions(id),
  turn_id TEXT REFERENCES turns(id), role TEXT NOT NULL, content TEXT, at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS messages_session ON messages(session_id, at);`
		if _, err := db.Exec(msgs); err != nil {
			return fmt.Errorf("creating messages table: %w", err)
		}
		if _, err := db.Exec("INSERT INTO schema_version(version) VALUES(8)"); err != nil {
			return fmt.Errorf("recording messages migration: %w", err)
		}
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS messages_turn ON messages(turn_id, at)"); err != nil {
		return fmt.Errorf("indexing messages by turn: %w", err)
	}
	var n9 int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=9").Scan(&n9); err != nil {
		return fmt.Errorf("checking message search migration: %w", err)
	}
	if n9 == 0 {
		const messageFTS = `
CREATE VIRTUAL TABLE IF NOT EXISTS message_fts USING fts5(content, content='messages', content_rowid='rowid');
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
  INSERT INTO message_fts(rowid,content) VALUES(new.rowid,new.content);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
  INSERT INTO message_fts(message_fts,rowid,content) VALUES('delete',old.rowid,old.content);
END;
CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
  INSERT INTO message_fts(message_fts,rowid,content) VALUES('delete',old.rowid,old.content);
  INSERT INTO message_fts(rowid,content) VALUES(new.rowid,new.content);
END;`
		if _, err := db.Exec(messageFTS); err != nil {
			return fmt.Errorf("creating message search: %w", err)
		}
		if _, err := db.Exec("INSERT INTO message_fts(message_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("indexing messages: %w", err)
		}
		if _, err := db.Exec("INSERT INTO schema_version(version) VALUES(9)"); err != nil {
			return fmt.Errorf("recording message search migration: %w", err)
		}
	}
	var n10 int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=10").Scan(&n10); err != nil {
		return fmt.Errorf("checking trails migration: %w", err)
	}
	if n10 == 0 {
		const trails = `
CREATE TABLE IF NOT EXISTS trails (id TEXT PRIMARY KEY, title TEXT NOT NULL, kind TEXT NOT NULL,
  base_step INTEGER NOT NULL, head_step INTEGER NOT NULL, created_at INTEGER NOT NULL);`
		if _, err := db.Exec(trails); err != nil {
			return fmt.Errorf("creating trails table: %w", err)
		}
		if _, err := db.Exec("INSERT INTO schema_version(version) VALUES(10)"); err != nil {
			return fmt.Errorf("recording trails migration: %w", err)
		}
	}
	var n11 int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=11").Scan(&n11); err != nil {
		return fmt.Errorf("checking usage migration: %w", err)
	}
	if n11 == 0 {
		for _, stmt := range []string{
			"ALTER TABLE turns ADD COLUMN tokens_in INTEGER",
			"ALTER TABLE turns ADD COLUMN tokens_out INTEGER",
			"ALTER TABLE turns ADD COLUMN cost_usd REAL",
		} {
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("adding usage columns: %w", err)
			}
		}
		if _, err := db.Exec("INSERT INTO schema_version(version) VALUES(11)"); err != nil {
			return fmt.Errorf("recording usage migration: %w", err)
		}
	}
	var n12 int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=12").Scan(&n12); err != nil {
		return fmt.Errorf("checking comments migration: %w", err)
	}
	if n12 == 0 {
		const comments = `
CREATE TABLE IF NOT EXISTS comments (id TEXT PRIMARY KEY, file TEXT NOT NULL, line INTEGER NOT NULL,
  body TEXT NOT NULL, turn_id TEXT REFERENCES turns(id), session_id TEXT, at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS comments_file ON comments(file, line);`
		if _, err := db.Exec(comments); err != nil {
			return fmt.Errorf("creating comments table: %w", err)
		}
		if _, err := db.Exec("INSERT INTO schema_version(version) VALUES(12)"); err != nil {
			return fmt.Errorf("recording comments migration: %w", err)
		}
	}
	var n13 int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=13").Scan(&n13); err != nil {
		return fmt.Errorf("checking causal migration: %w", err)
	}
	if n13 == 0 {
		if _, err := db.Exec("ALTER TABLE ops ADD COLUMN parent_op_id TEXT REFERENCES ops(id)"); err != nil {
			return fmt.Errorf("adding parent_op_id: %w", err)
		}
		if _, err := db.Exec("CREATE INDEX IF NOT EXISTS ops_parent ON ops(parent_op_id)"); err != nil {
			return fmt.Errorf("indexing parent ops: %w", err)
		}
		if _, err := db.Exec("INSERT INTO schema_version(version) VALUES(13)"); err != nil {
			return fmt.Errorf("recording causal migration: %w", err)
		}
	}
	return nil
}
