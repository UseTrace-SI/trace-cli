package syncclient

import (
	"database/sql"
	"encoding/json"
	"github.com/UseTrace-SI/trace-cli/internal/store"
	"path/filepath"
	"testing"
)

func TestRecoveryRetainsUsageAndAttribution(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex','repo',1)"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ table, id, data string }{
		{"turns", "t", `{"id":"t","session_id":"s","seq":1,"started_at":1,"tokens_in":42,"tokens_out":24,"cost_usd":0.25}`},
		{"ops", "a", `{"id":"a","turn_id":"t","session_id":"s","path":"file.go","kind":"modify","source":"tool","actor":"codex","at":1}`},
		{"ops", "b", `{"id":"b","turn_id":"t","session_id":"s","path":"file.go","kind":"modify","source":"tool","actor":"human","parent_op_id":"a","at":2}`},
	} {
		if err := insertDownloaded(db.DB, Row{Table: tc.table, ID: tc.id, Data: json.RawMessage(tc.data)}, []byte(tc.data)); err != nil {
			t.Fatal(err)
		}
	}
	var in, out sql.NullInt64
	var cost sql.NullFloat64
	var actor, parent sql.NullString
	if err := db.QueryRow("SELECT tokens_in,tokens_out,cost_usd FROM turns WHERE id='t'").Scan(&in, &out, &cost); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT actor,parent_op_id FROM ops WHERE id='b'").Scan(&actor, &parent); err != nil {
		t.Fatal(err)
	}
	if !in.Valid || in.Int64 != 42 || !out.Valid || out.Int64 != 24 || !cost.Valid || cost.Float64 != 0.25 || actor.String != "human" || parent.String != "a" {
		t.Fatalf("metadata lost: %+v %+v %+v %+v %+v", in, out, cost, actor, parent)
	}
	correction := []byte(`{"id":"u","table_name":"turns","row_id":"t","data":{"id":"t","prompt":"done","reasoning":"context","tool_calls":"","files_read":"","ended_at":2,"tokens_in":100,"tokens_out":50,"cost_usd":0.5}}`)
	if err := insertDownloaded(db.DB, Row{Table: "sync_updates", ID: "u"}, correction); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT tokens_in FROM turns WHERE id='t'").Scan(&in); err != nil || in.Int64 != 100 {
		t.Fatalf("corrected usage lost: %+v %v", in, err)
	}
}
