package store

import (
	"path/filepath"
	"testing"
)

func TestOpenTwice(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trace.db")
	for i := 0; i < 2; i++ {
		db, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.QueryRow("SELECT count(*) FROM schema_version").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 10 {
			t.Fatalf("schema versions: %d", n)
		}
		if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('ops') WHERE name='detail'").Scan(&n); err != nil || n != 1 {
			t.Fatalf("structural detail column: %d, %v", n, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMessageSearchMigrationRebuildsAndTracksChanges(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trace.db")
	db, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex','.',1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO turns(id,session_id,seq,started_at) VALUES('t','s',1,1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO messages(id,session_id,turn_id,role,content,at) VALUES('m','s','t','user','migration needle',1)"); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"DROP TRIGGER messages_ai", "DROP TRIGGER messages_ad", "DROP TRIGGER messages_au",
		"DROP TABLE message_fts", "DELETE FROM schema_version WHERE version=9",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	count := func(term string) int {
		t.Helper()
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM message_fts WHERE message_fts MATCH ?", term).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := count("needle"); got != 1 {
		t.Fatalf("rebuild found %d messages, want 1", got)
	}
	if _, err := db.Exec("UPDATE messages SET content='updated signal' WHERE id='m'"); err != nil {
		t.Fatal(err)
	}
	if got := count("needle"); got != 0 {
		t.Fatalf("stale message index has %d hits", got)
	}
	if got := count("signal"); got != 1 {
		t.Fatalf("updated message index has %d hits", got)
	}
	if _, err := db.Exec("DELETE FROM messages WHERE id='m'"); err != nil {
		t.Fatal(err)
	}
	if got := count("signal"); got != 0 {
		t.Fatalf("deleted message index has %d hits", got)
	}
}
