package opencode

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestPollUpdatedPartsAndTurnBoundary(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT, message_id TEXT, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session VALUES ('s','/repo'),('other','/other')`,
		`INSERT INTO message VALUES ('user','{"role":"user"}'),('assistant','{"role":"assistant"}')`,
		`INSERT INTO part VALUES ('p','s','user',1,'{"type":"text","text":"fix"}')`,
		`INSERT INTO part VALUES ('t','s','assistant',2,'{"type":"tool","tool":"edit","state":{"status":"running"}}')`,
		`INSERT INTO part VALUES ('f','s','assistant',3,'{"type":"step-finish","reason":"tool-calls"}')`,
		`INSERT INTO part VALUES ('ignore','other','user',3,'{"type":"text","text":"other"}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var cursor pollCursor
	var got []event.Event
	consume := func(ev event.Event) error { got = append(got, ev); return nil }
	if err := poll(context.Background(), db, "/repo", &cursor, consume); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != event.TurnStart || got[0].Prompt != "fix" {
		t.Fatalf("initial events: %+v", got)
	}
	if _, err := db.Exec(`UPDATE part SET time_updated=4,data='{"type":"tool","tool":"edit","state":{"status":"completed","input":{"filePath":"main.go"}}}' WHERE id='t'`); err != nil {
		t.Fatal(err)
	}
	if err := poll(context.Background(), db, "/repo", &cursor, consume); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Kind != event.ToolEdit || len(got[1].Paths) != 1 || got[1].Paths[0] != "main.go" {
		t.Fatalf("completed tool: %+v", got)
	}
	// A new row at the same timestamp and a partial reasoning update must not be lost.
	if _, err := db.Exec(`INSERT INTO part VALUES ('r','s','assistant',4,'{"type":"reasoning","text":"partial","time":{"start":4}}')`); err != nil {
		t.Fatal(err)
	}
	if err := poll(context.Background(), db, "/repo", &cursor, consume); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE part SET data='{"type":"reasoning","text":"finished","time":{"start":4,"end":4}}' WHERE id='r'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO part VALUES ('z','s','assistant',4,'{"type":"step-finish","reason":"stop"}')`); err != nil {
		t.Fatal(err)
	}
	if err := poll(context.Background(), db, "/repo", &cursor, consume); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[2].Kind != event.TurnContext || got[2].Text != "finished" || got[3].Kind != event.TurnEnd {
		t.Fatalf("same-timestamp events: %+v", got)
	}
	if err := poll(context.Background(), db, "/repo", &cursor, consume); err != nil || len(got) != 4 {
		t.Fatalf("duplicate poll: %+v %v", got, err)
	}
}

func TestPollRetriesProcessFailure(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{`CREATE TABLE session (id TEXT, directory TEXT)`, `CREATE TABLE message (id TEXT, data TEXT)`, `CREATE TABLE part (id TEXT, session_id TEXT, message_id TEXT, time_updated INTEGER, data TEXT)`, `INSERT INTO session VALUES ('s','/repo')`, `INSERT INTO message VALUES ('m','{"role":"user"}')`, `INSERT INTO part VALUES ('p','s','m',1,'{"type":"text","text":"hello"}')`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var cursor pollCursor
	if err := poll(context.Background(), db, "/repo", &cursor, func(event.Event) error { return errors.New("retry") }); err == nil {
		t.Fatal("missing process error")
	}
	n := 0
	if err := poll(context.Background(), db, "/repo", &cursor, func(event.Event) error { n++; return nil }); err != nil || n != 1 {
		t.Fatalf("retry count %d: %v", n, err)
	}
}

func TestRunDoesNotCreateMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := Run(ctx, t.TempDir(), path, func(event.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("database was created: %v", err)
	}
}

func TestCursorScopedToRepositoryAndLateUpdates(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE session (id TEXT, directory TEXT)`,
		`CREATE TABLE message (id TEXT, data TEXT)`,
		`CREATE TABLE part (id TEXT, session_id TEXT, message_id TEXT, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session VALUES ('s','/repo'),('other','/other')`,
		`INSERT INTO message VALUES ('u','{"role":"user"}')`,
		`INSERT INTO part VALUES ('old','s','u',100000,'{"type":"text","text":"old"}'),('other','other','u',999999,'{"type":"text","text":"other"}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var cursor pollCursor
	if err := initializeCursor(context.Background(), db, "/repo", &cursor); err != nil {
		t.Fatal(err)
	}
	if cursor.updated != 100000 {
		t.Fatalf("cursor includes another repo: %d", cursor.updated)
	}
	var got []event.Event
	consume := func(ev event.Event) error { got = append(got, ev); return nil }
	if err := poll(context.Background(), db, "/repo", &cursor, consume); err != nil || len(got) != 0 {
		t.Fatalf("imported history: %+v %v", got, err)
	}
	if _, err := db.Exec(`INSERT INTO part VALUES ('new','s','u',100010,'{"type":"text","text":"new"}')`); err != nil {
		t.Fatal(err)
	}
	if err := poll(context.Background(), db, "/repo", &cursor, consume); err != nil {
		t.Fatal(err)
	}
	// A row committed later with an earlier timestamp still belongs to this run.
	if _, err := db.Exec(`INSERT INTO part VALUES ('late','s','u',100005,'{"type":"text","text":"late"}')`); err != nil {
		t.Fatal(err)
	}
	if err := poll(context.Background(), db, "/repo", &cursor, consume); err != nil {
		t.Fatal(err)
	}
	if err := poll(context.Background(), db, "/repo", &cursor, consume); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Prompt != "new" || got[1].Prompt != "late" {
		t.Fatalf("events: %+v", got)
	}
}

func TestRunWaitsForSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE placeholder (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	events := make(chan event.Event, 1)
	go func() { done <- Run(ctx, "/repo", path, func(ev event.Event) error { events <- ev; return nil }) }()
	select {
	case err := <-done:
		t.Fatalf("poller exited before schema: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	for _, q := range []string{
		`CREATE TABLE session (id TEXT, directory TEXT)`,
		`CREATE TABLE message (id TEXT, data TEXT)`,
		`CREATE TABLE part (id TEXT, session_id TEXT, message_id TEXT, time_updated INTEGER, data TEXT)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-done:
		t.Fatalf("poller exited after schema: %v", err)
	case <-time.After(1200 * time.Millisecond):
	}
	for _, q := range []string{
		`INSERT INTO session VALUES ('s','/repo')`,
		`INSERT INTO message VALUES ('u','{"role":"user"}')`,
		`INSERT INTO part VALUES ('p','s','u',100,'{"type":"text","text":"after migration"}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case ev := <-events:
		if ev.Prompt != "after migration" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case err := <-done:
		t.Fatalf("poller exited: %v", err)
	case <-time.After(4 * time.Second):
		t.Fatal("poller did not record after migration")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("poller did not stop")
	}
}
