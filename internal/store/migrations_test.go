package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func countColumn(t *testing.T, db *sql.DB, table, column string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('"+table+"') WHERE name=?", column).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPartialAlterMigrationIsReplayedIdempotently(t *testing.T) {
	for _, tc := range []struct {
		version       int
		table, column string
	}{
		{4, "ops", "detail"},
		{7, "ops", "actor"},
		{11, "turns", "tokens_in"},
		{13, "ops", "parent_op_id"},
	} {
		t.Run(fmt.Sprintf("v%d", tc.version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "trace.db")
			db, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			// Simulate an older build that applied the ALTER but crashed before
			// recording the version. The column already exists.
			if _, err := db.Exec("DELETE FROM schema_version WHERE version=?", tc.version); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(path)
			if err != nil {
				t.Fatalf("reopening a partially migrated database: %v", err)
			}
			defer db.Close()
			var n int
			if err := db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=?", tc.version).Scan(&n); err != nil || n != 1 {
				t.Fatalf("version %d not recorded: %d, %v", tc.version, n, err)
			}
			if got := countColumn(t, db.DB, tc.table, tc.column); got != 1 {
				t.Fatalf("column %s count = %d, want 1", tc.column, got)
			}
		})
	}
}

func TestAlterMigrationRollsBackWithoutVersionInsert(t *testing.T) {
	for _, tc := range []struct {
		version       int
		table, column string
		undo          []string
	}{
		{4, "ops", "detail", []string{"ALTER TABLE ops DROP COLUMN detail"}},
		{7, "ops", "actor", []string{"ALTER TABLE ops DROP COLUMN actor"}},
		{11, "turns", "tokens_in", []string{
			"ALTER TABLE turns DROP COLUMN tokens_in",
			"ALTER TABLE turns DROP COLUMN tokens_out",
			"ALTER TABLE turns DROP COLUMN cost_usd",
		}},
		{13, "ops", "parent_op_id", []string{
			"DROP INDEX ops_parent",
			"ALTER TABLE ops DROP COLUMN parent_op_id",
		}},
	} {
		t.Run(fmt.Sprintf("v%d", tc.version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "trace.db")
			db, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, stmt := range tc.undo {
				if _, err := db.Exec(stmt); err != nil {
					t.Fatalf("%s: %v", stmt, err)
				}
			}
			if _, err := db.Exec("DELETE FROM schema_version WHERE version=?", tc.version); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(fmt.Sprintf("CREATE TRIGGER block_version BEFORE INSERT ON schema_version WHEN NEW.version=%d BEGIN SELECT RAISE(ABORT, 'blocked'); END;", tc.version)); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path); err == nil {
				t.Fatal("migration succeeded even though its version insert failed")
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			var n int
			if err := raw.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version=?", tc.version).Scan(&n); err != nil || n != 0 {
				t.Fatalf("version %d was recorded after failure: %d, %v", tc.version, n, err)
			}
			if got := countColumn(t, raw, tc.table, tc.column); got != 0 {
				t.Fatalf("column %s survived a rolled back migration: %d", tc.column, got)
			}
			if _, err := raw.Exec("DROP TRIGGER block_version"); err != nil {
				t.Fatal(err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(path)
			if err != nil {
				t.Fatalf("reopening after the failure was cleared: %v", err)
			}
			defer db.Close()
			if got := countColumn(t, db.DB, tc.table, tc.column); got != 1 {
				t.Fatalf("column %s was not added on retry: %d", tc.column, got)
			}
		})
	}
}
