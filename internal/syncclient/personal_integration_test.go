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
	cfg := Config{Endpoint: "https://usetrace.si", Remote: "solo/project", Mode: "personal", Device: true}
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
		flow, err := StartDeviceLogin(ctx, worker, cfg, "Integration laptop")
		if err != nil {
			t.Fatal(err)
		}
		if flow.VerificationURI != cfg.Endpoint+"/connect" {
			t.Fatalf("pairing did not use the clean approval route: %s", flow.VerificationURI)
		}
		response, _ := request("POST", "/api/cli/device/approve", cookie, map[string]any{"code": flow.UserCode})
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

	for _, statement := range []string{
		"UPDATE turns SET tokens_in=42,tokens_out=24,cost_usd=0.25 WHERE id='t1'",
		"INSERT INTO ops(id,turn_id,session_id,path,kind,source,actor,at) VALUES('o1','t1','s1','main.go','edit','tool','codex',2)",
		"INSERT INTO ops(id,turn_id,session_id,path,kind,source,actor,parent_op_id,at) VALUES('o2','t1','s1','main.go','edit','tool','human','o1',3)",
		"INSERT INTO comments(id,file,line,body,turn_id,session_id,at) VALUES('comment1','main.go',1,'reviewed','t1','s1',3)",
		"INSERT INTO trails(id,title,kind,base_step,head_step,created_at) VALUES('trail1','fix','work',1,1,3)",
	} {
		if _, err := db.Exec(statement); err != nil {
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
	if err != nil || rows != 8 {
		t.Fatalf("personal sync: %d, %v", rows, err)
	}
	other := *client
	other.Config.Remote = "solo/another-project"
	other.TraceDir = t.TempDir()
	if _, n, err := other.Sync(ctx); err != nil || n != 8 {
		t.Fatalf("second project with one login: %d %v", n, err)
	}
	response, listing := request("GET", "/api/repos?scope=personal", alice, nil)
	if response.StatusCode != 200 || len(listing["repos"].([]any)) != 2 {
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
	if _, n, err := client.Pull(ctx, target, ""); err != nil || n != 8 {
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
	var tokens int
	var actor, parent string
	if err := recovered.QueryRow("SELECT tokens_in FROM turns WHERE id='t1'").Scan(&tokens); err != nil || tokens != 42 {
		t.Fatalf("usage recovery: %d %v", tokens, err)
	}
	if err := recovered.QueryRow("SELECT actor,parent_op_id FROM ops WHERE id='o2'").Scan(&actor, &parent); err != nil || actor != "human" || parent != "o1" {
		t.Fatalf("ancestry recovery: %s %s %v", actor, parent, err)
	}
	for _, table := range []string{"comments", "trails"} {
		if err := recovered.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s recovery: %d %v", table, n, err)
		}
	}
	// Reconnecting the same checkout to a different account must resend history,
	// rather than carrying over the first account's upload cursor.
	credentials = connect(bob)
	client.Config.ConnectionID = credentials.ConnectionID
	client.Token = credentials.AccessToken
	if _, n, err := client.Sync(ctx); err != nil || n != 8 {
		t.Fatalf("account switch skipped history: %d %v", n, err)
	}
}
