package store

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestPortableHistoryCoversEveryColumn(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range HistoryTables {
		rows, err := db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var cid, nn, pk int
			var name, typ string
			var def any
			if err := rows.Scan(&cid, &name, &typ, &nn, &def, &pk); err != nil {
				t.Fatal(err)
			}
			got = append(got, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		want := append([]string(nil), HistoryColumns[table]...)
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s recovery schema omits data: got %v want %v", table, want, got)
		}
	}
}
