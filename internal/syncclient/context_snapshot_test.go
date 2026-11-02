package syncclient

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/store"
)

func TestRecoveredCompanionContextRedactsBeforeStorage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	payload := `{"schema":1,"brain":{"memories":[{"content":"token ` + secret + `"}]}}`
	plain, _ := json.Marshal(map[string]any{"id": "snapshot1", "source_step": 1, "created_at": 2, "payload": payload, "fingerprint": "original"})
	if err := insertDownloaded(db.DB, Row{Table: "context_snapshots", ID: "snapshot1"}, plain); err != nil {
		t.Fatal(err)
	}
	var stored, fingerprint string
	if err := db.QueryRow("SELECT payload,fingerprint FROM context_snapshots").Scan(&stored, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, secret) || !json.Valid([]byte(stored)) || len(fingerprint) != 64 {
		t.Fatalf("unsafe recovered context: %s", stored)
	}
	// Redaction can merge two derived cache entries. That must not prevent
	// recovery of the canonical history that precedes them.
	plain, _ = json.Marshal(map[string]any{"id": "snapshot2", "source_step": 1, "created_at": 3, "payload": payload, "fingerprint": "other"})
	if err := insertDownloaded(db.DB, Row{Table: "context_snapshots", ID: "snapshot2"}, plain); err != nil {
		t.Fatal(err)
	}
}
