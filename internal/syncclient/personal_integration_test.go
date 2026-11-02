//go:build syncintegration

package syncclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestPersonalAccountConnectionSyncDashboardAndRecovery(t *testing.T) {
	worker := startWorkerBridge(t)
	ctx := context.Background()
	cfg := Config{Endpoint: "https://usetrace.si", Remote: "solo/project", Mode: "personal"}
	request := func(method, path, cookie string, body any) (*http.Response, map[string]any) {
		t.Helper()
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req, err := http.NewRequest(method, cfg.Endpoint+path, bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", cfg.Endpoint)
		req.Header.Set("Cookie", cookie)
		response, err := worker.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("invalid response %d: %s", response.StatusCode, data)
		}
		return response, result
	}
	signup := func(name string) string {
		response, _ := request("POST", "/auth/password/signup", "", map[string]string{"name": name, "email": name + "@example.com", "password": "long-integration-passphrase"})
		if response.StatusCode != 201 {
			t.Fatalf("signup: %d", response.StatusCode)
		}
		return strings.Split(response.Header.Get("Set-Cookie"), ";")[0]
	}
	connect := func(cookie string) Credentials {
		flow, err := StartPairing(ctx, worker, cfg, "Integration laptop")
		if err != nil {
			t.Fatal(err)
		}
		response, _ := request("POST", "/api/cli/pairings/"+flow.ID, cookie, map[string]any{})
		if response.StatusCode != 200 {
			t.Fatalf("approval: %d", response.StatusCode)
		}
		creds, err := WaitPairing(ctx, worker, cfg.Endpoint, flow)
		if err != nil {
			t.Fatal(err)
		}
		return creds
	}
	alice := signup("alice")
	bob := signup("bob")
	credentials := connect(alice)
	cfg.ConnectionID = credentials.ConnectionID
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
	file, err := bs.PutBytes([]byte("package main\nfunc main() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := (manifest.Manifest{"main.go": {Hash: file, Mode: 0600}}).Save(bs)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s1','codex','repo',1)", nil},
		{"INSERT INTO turns(id,session_id,seq,prompt,started_at,ended_at) VALUES('t1','s1',1,'Private personal turn',1,2)", nil},
		{"INSERT INTO keyframes(id,turn_id,seq,manifest_hash,tag,created_at) VALUES('k1','t1',1,?,'turn',2)", []any{tree}},
	} {
		if _, err := db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	payload := `{"schema":1,"brain":{"status":"ready","memories":[]},"graph":{"status":"ready","symbols":[],"relations":[]}}`
	sum := sha256.Sum256([]byte(payload))
	if _, err := db.Exec("INSERT INTO context_snapshots(id,source_step,created_at,payload,fingerprint) VALUES('c1',1,3,?,?)", payload, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	client := &Client{HTTP: worker, Config: cfg, Token: credentials.AccessToken, TraceDir: dir, DB: db.DB, Blobs: bs}
	_, rows, err := client.Sync(ctx)
	if err != nil || rows != 4 {
		t.Fatalf("personal sync: %d, %v", rows, err)
	}
	response, listing := request("GET", "/api/repos?scope=personal", alice, nil)
	if response.StatusCode != 200 || len(listing["repos"].([]any)) != 1 {
		t.Fatalf("dashboard list: %+v", listing)
	}
	_, listing = request("GET", "/api/repos?scope=personal", bob, nil)
	if len(listing["repos"].([]any)) != 0 {
		t.Fatal("another account saw personal history")
	}
	response, detail := request("GET", "/api/repos/solo/project/sessions/s1?mode=personal", alice, nil)
	if response.StatusCode != 200 || detail["turns"].([]any)[0].(map[string]any)["prompt"] != "Private personal turn" {
		t.Fatalf("dashboard detail: %+v", detail)
	}
	target := filepath.Join(t.TempDir(), ".trace")
	if _, n, err := client.Pull(ctx, target, ""); err != nil || n != 4 {
		t.Fatalf("personal recovery: %d, %v", n, err)
	}
	recovered, err := store.Open(filepath.Join(target, "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	recoveredBlobs, err := blob.Open(filepath.Join(target, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	recoveredTree, err := manifest.Load(tree, recoveredBlobs)
	if err != nil || recoveredTree["main.go"].Hash != file {
		t.Fatalf("manifest recovery: %v %v", recoveredTree, err)
	}
	reader, err := recoveredBlobs.Get(file)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(contents) != "package main\nfunc main() {}\n" {
		t.Fatalf("file recovery: %q %v", contents, err)
	}
	var n int
	if err := recovered.QueryRow("SELECT COUNT(*) FROM context_snapshots").Scan(&n); err != nil || n != 1 {
		t.Fatalf("recovered context: %d %v", n, err)
	}
	// Reconnecting the same checkout to a different account must resend history,
	// rather than carrying over the first account's upload cursor.
	credentials = connect(bob)
	client.Config.ConnectionID = credentials.ConnectionID
	client.Token = credentials.AccessToken
	if _, n, err := client.Sync(ctx); err != nil || n != 4 {
		t.Fatalf("account switch skipped history: %d %v", n, err)
	}
}
