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
		if n != 5 {
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
