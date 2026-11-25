package syncclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

// A keyframe recorded before .npmrc was excluded lists it, and an operation
// stored its bytes. Uploads must carry neither the bytes nor a manifest that
// names the file, while staying self-consistent; the local history is kept.
func TestSyncLeavesOutSinceExcludedHistory(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bs, err := blob.Open(filepath.Join(dir, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	secretBytes := []byte("//registry.example/:_authToken=npm_old0123456789\n")
	app, err := bs.PutBytes([]byte("package app\n"))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := bs.PutBytes(secretBytes)
	if err != nil {
		t.Fatal(err)
	}
	original, err := (manifest.Manifest{"app.go": {Hash: app, Mode: 0644}, ".npmrc": {Hash: secret, Mode: 0644}}).Save(bs)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) VALUES('K1',1,'" + string(original) + "','turn',1)",
		"INSERT INTO ops(id,path,kind,after_hash,source,at) VALUES('O1','app.go','add','" + string(app) + "','hook',1)",
		"INSERT INTO ops(id,path,kind,after_hash,source,at) VALUES('O2','.npmrc','add','" + string(secret) + "','hook',1)",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	// A correction snapshots the whole operation row, hashes included.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUpdate(tx, "ops", "O2"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	offered := map[string]bool{}
	uploaded := map[string]bool{}
	rows := map[string]map[string]any{}
	client := &Client{Config: Config{Endpoint: "https://sync.example", Remote: "owner/repo", Mode: "team"}, Token: "test-token", TraceDir: dir, DB: db.DB, Blobs: bs}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("_authToken")) || bytes.Contains(body, []byte(".npmrc\":{")) {
			t.Fatalf("request to %s carries .npmrc content: %s", r.URL.Path, body)
		}
		switch r.URL.Path {
		case "/v1/sync/blobs/have":
			var v struct{ Hashes []string }
			if err := json.Unmarshal(body, &v); err != nil {
				t.Fatal(err)
			}
			for _, h := range v.Hashes {
				offered[h] = true
			}
			need, _ := json.Marshal(map[string]any{"need": v.Hashes})
			return response(string(need)), nil
		case "/v1/sync/blobs":
			var v struct{ Hash string }
			if err := json.Unmarshal(body, &v); err != nil {
				t.Fatal(err)
			}
			uploaded[v.Hash] = true
			return response(`{}`), nil
		case "/v1/sync/rows":
			var v struct{ Rows []Row }
			if err := json.Unmarshal(body, &v); err != nil {
				t.Fatal(err)
			}
			for _, row := range v.Rows {
				var data map[string]any
				if err := json.Unmarshal(row.Data, &data); err != nil {
					t.Fatal(err)
				}
				rows[row.Table+":"+row.ID] = data
			}
			return response(`{}`), nil
		}
		return response(`{"rows":[],"next":null}`), nil
	})}
	if _, _, err := client.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if offered[string(secret)] || uploaded[string(secret)] || offered[string(original)] {
		t.Fatalf("upload offered the .npmrc bytes or the manifest naming it: offered %v", offered)
	}
	keyframe := rows["keyframes:K1"]
	stripped, _ := keyframe["manifest_hash"].(string)
	if stripped == "" || stripped == string(original) || !uploaded[stripped] {
		t.Fatalf("keyframe uploaded with manifest %q (original %s, uploaded %v)", stripped, original, uploaded[stripped])
	}
	m, err := manifest.Load(blob.Hash(stripped), bs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m[".npmrc"]; ok || m["app.go"].Hash != app || !uploaded[string(app)] {
		t.Fatalf("uploaded snapshot %v", m)
	}
	if op := rows["ops:O2"]; op == nil || op["after_hash"] != nil {
		t.Fatalf(".npmrc operation uploaded with content: %v", op)
	}
	if op := rows["ops:O1"]; op == nil || op["after_hash"] != string(app) {
		t.Fatalf("app.go operation lost its content: %v", op)
	}
	var correction map[string]any
	for key, row := range rows {
		if strings.HasPrefix(key, "sync_updates:") && row["row_id"] == "O2" {
			correction, _ = row["data"].(map[string]any)
		}
	}
	if correction == nil || correction["path"] != ".npmrc" || correction["after_hash"] != nil || correction["before_hash"] != nil {
		t.Fatalf(".npmrc operation correction uploaded with content: %v", correction)
	}
	var local string
	if err := db.QueryRow("SELECT manifest_hash FROM keyframes WHERE id='K1'").Scan(&local); err != nil || local != string(original) {
		t.Fatalf("sync changed local history: %s %v", local, err)
	}
}
