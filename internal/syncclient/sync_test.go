package syncclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestReadCommitPages(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, commit := range []struct {
		sha string
		at  int64
	}{{"c", 101}, {"b", 100}, {"e", 103}, {"a", 100}, {"d", 102}} {
		if _, err := db.Exec("INSERT INTO commits(sha, at) VALUES(?, ?)", commit.sha, commit.at); err != nil {
			t.Fatal(err)
		}
	}
	var index string
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='commits_at_sha'").Scan(&index); err != nil {
		t.Fatalf("commit cursor index: %v", err)
	}
	var got []string
	after := ""
	for {
		rows, cursors, err := readRows(db.DB, "commits", after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != len(cursors) || len(rows) > 2 {
			t.Fatalf("invalid page: %d rows, %d cursors", len(rows), len(cursors))
		}
		for i, row := range rows {
			got = append(got, row.ID)
			after = cursors[i]
		}
		if len(rows) < 2 {
			break
		}
	}
	if want := []string{"a", "b", "c", "d", "e"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commit pages = %v, want %v", got, want)
	}
	if _, _, err := readRows(db.DB, "commits", "bad-cursor", 2); err == nil {
		t.Fatal("invalid commit cursor accepted")
	}
}

func TestCipherRoundTripAndAuthentication(t *testing.T) {
	c, err := Derive("a sufficiently long passphrase", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := c.Seal([]byte("private history"), "turn:one")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "private history") {
		t.Fatal("plaintext in ciphertext")
	}
	plain, err := c.Open(encrypted, "turn:one")
	if err != nil || string(plain) != "private history" {
		t.Fatalf("round trip: %q, %v", plain, err)
	}
	if _, err := c.Open(encrypted, "turn:two"); err == nil {
		t.Fatal("wrong associated data accepted")
	}
	if _, err := c.Open(encrypted[:len(encrypted)-3], "turn:one"); err == nil {
		t.Fatal("truncated ciphertext accepted")
	}
}

func TestInsertDownloadedIsIdempotent(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	session := Row{Table: "sessions", ID: "SESSION1"}
	sessionData := []byte(`{"id":"SESSION1","agent":"codex","worktree":"repo","started_at":1}`)
	for i := 0; i < 2; i++ {
		if err := insertDownloaded(db.DB, session, sessionData); err != nil {
			t.Fatalf("insert session pass %d: %v", i, err)
		}
	}
	update := Row{Table: "sync_updates", ID: "UPDATE1"}
	updateData := []byte(`{"id":"UPDATE1","table_name":"sessions","row_id":"SESSION1","data":{"id":"SESSION1","ended_at":5}}`)
	for i := 0; i < 2; i++ {
		if err := insertDownloaded(db.DB, update, updateData); err != nil {
			t.Fatalf("apply update pass %d: %v", i, err)
		}
	}
	var sessions, updates int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM sync_updates").Scan(&updates); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || updates != 1 {
		t.Fatalf("sessions=%d updates=%d", sessions, updates)
	}
	var ended int64
	if err := db.QueryRow("SELECT ended_at FROM sessions WHERE id='SESSION1'").Scan(&ended); err != nil || ended != 5 {
		t.Fatalf("ended_at=%d err=%v", ended, err)
	}
}

func TestPullRemapsSharedStepsAndRedactsText(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"SESSION1", "SESSION2"} {
		row := Row{Table: "sessions", ID: id}
		data := []byte(fmt.Sprintf(`{"id":%q,"agent":"codex","worktree":"repo","started_at":1}`, id))
		if err := insertDownloaded(db.DB, row, data); err != nil {
			t.Fatal(err)
		}
	}
	for index, sessionID := range []string{"SESSION1", "SESSION2"} {
		id := fmt.Sprintf("TURN%d", index)
		row := Row{Table: "turns", ID: id}
		data := []byte(fmt.Sprintf(`{"id":%q,"session_id":%q,"seq":1,"prompt":"ghp_abcdefghijklmnopqrstuvwxyz","started_at":1}`, id, sessionID))
		if err := insertDownloaded(db.DB, row, data); err != nil {
			t.Fatal(err)
		}
	}
	var first, second int
	if err := db.QueryRow("SELECT seq FROM turns WHERE id='TURN0'").Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT seq FROM turns WHERE id='TURN1'").Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 {
		t.Fatalf("shared steps collided: %d, %d", first, second)
	}
	var prompt string
	if err := db.QueryRow("SELECT prompt FROM turns WHERE id='TURN0'").Scan(&prompt); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "ghp_") {
		t.Fatal("downloaded secret was stored")
	}
}

func TestSyncBatchesCorrectionsForWorkerIndexBudget(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('SESSION','codex','repo',1)"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for range 25 {
		if err := store.RecordUpdate(tx, "sessions", "SESSION"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	bs, err := blob.Open(filepath.Join(dir, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	var updateBatches []int
	client := &Client{Config: Config{Endpoint: "https://sync.example", Remote: "owner/repo", Mode: "team"},
		Token: "token", TraceDir: dir, DB: db.DB, Blobs: bs}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request struct {
			Rows []Row `json:"rows"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Rows) > 0 && request.Rows[0].Table == "sync_updates" {
			updateBatches = append(updateBatches, len(request.Rows))
		}
		return response(`{}`), nil
	})}
	_, rows, err := client.Sync(context.Background())
	if err != nil || rows != 26 {
		t.Fatalf("synced rows=%d, err=%v", rows, err)
	}
	if !reflect.DeepEqual(updateBatches, []int{20, 5}) {
		t.Fatalf("correction batch sizes = %v", updateBatches)
	}
}

func TestDownloadAcceptsFullWorkerPage(t *testing.T) {
	client := &Client{Config: Config{Endpoint: "https://sync.example", Remote: "owner/repo", Mode: "team"}, Token: "test-token"}
	want := strings.Repeat("x", 17<<20)
	client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(`{"payload":"` + want + `"}`), nil
	})}
	var got struct {
		Payload string `json:"payload"`
	}
	if err := client.get(context.Background(), "/v1/sync/rows", client.downloadValues(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Payload != want {
		t.Fatal("downloaded page was truncated")
	}
}

func TestSyncSplitsLargeRowBatches(t *testing.T) {
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
	detail := strings.Repeat("x", 950_000)
	for i := range 10 {
		if _, err := db.Exec("INSERT INTO ops(id,path,kind,source,at,detail) VALUES(?,?,?,?,?,?)", fmt.Sprintf("OP%02d", i), "file.go", "edit", "tool", i, detail); err != nil {
			t.Fatal(err)
		}
	}
	requests := 0
	accepted := 0
	failOnce := true
	client := &Client{Config: Config{Endpoint: "https://sync.example", Remote: "owner/repo", Mode: "team"}, Token: "test-token", TraceDir: dir, DB: db.DB, Blobs: bs}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) > 8<<20 {
			t.Fatalf("row batch exceeded Worker limit: %d bytes", len(body))
		}
		var upload struct {
			Rows []Row `json:"rows"`
		}
		if err := json.Unmarshal(body, &upload); err != nil {
			t.Fatal(err)
		}
		requests++
		if failOnce && requests == 2 {
			failOnce = false
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable", Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}
		accepted += len(upload.Rows)
		return response(`{}`), nil
	})}
	_, firstRows, err := client.Sync(context.Background())
	if err == nil || firstRows == 0 || firstRows >= 10 {
		t.Fatalf("first sync should stop after its first acknowledged batch: rows=%d err=%v", firstRows, err)
	}
	_, remainingRows, err := client.Sync(context.Background())
	if err != nil || firstRows+remainingRows != 10 || accepted != 10 || requests < 3 {
		t.Fatalf("resumed sync: first=%d remaining=%d accepted=%d requests=%d err=%v", firstRows, remainingRows, accepted, requests, err)
	}
}

func TestSyncKeepsRowBatchesWithinD1FreeLimit(t *testing.T) {
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
	for i := range 81 {
		if _, err := db.Exec("INSERT INTO ops(id,path,kind,source,at,detail) VALUES(?,?,?,?,?,?)", fmt.Sprintf("OP%03d", i), "file.go", "edit", "tool", i, "short"); err != nil {
			t.Fatal(err)
		}
	}
	var counts []int
	client := &Client{Config: Config{Endpoint: "https://sync.example", Remote: "owner/repo", Mode: "team"}, Token: "test-token", TraceDir: dir, DB: db.DB, Blobs: bs}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var upload struct {
			Rows []Row `json:"rows"`
		}
		if err := json.NewDecoder(r.Body).Decode(&upload); err != nil {
			t.Fatal(err)
		}
		counts = append(counts, len(upload.Rows))
		return response(`{}`), nil
	})}
	_, rows, err := client.Sync(context.Background())
	if err != nil || rows != 81 {
		t.Fatalf("sync rows=%d err=%v", rows, err)
	}
	if !reflect.DeepEqual(counts, []int{40, 40, 1}) {
		t.Fatalf("row batch sizes: %v", counts)
	}
}

func TestSyncEncryptedRowsAndBlobs(t *testing.T) {
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
	hash, err := bs.PutBytes([]byte("source code"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('01JTEST','codex','repo',1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO ops(id,path,kind,after_hash,source,at) VALUES('01JOP','file.go','add',?,'watcher',2)", hash); err != nil {
		t.Fatal(err)
	}
	cipher, err := Derive("a sufficiently long passphrase", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	var rowUploads, blobUploads int
	client := &Client{Config: Config{Endpoint: "https://sync.example", Remote: "owner/repo", Mode: "pro"}, Token: "test-token", Cipher: cipher, TraceDir: dir, DB: db.DB, Blobs: bs}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("missing authorization")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("source code")) {
			t.Fatal("blob plaintext in request")
		}
		switch r.URL.Path {
		case "/v1/sync/blobs/have":
			return response(`{"need":["` + string(hash) + `"]}`), nil
		case "/v1/sync/blobs":
			blobUploads++
			var v struct {
				Payload string `json:"payload"`
				Salt    string `json:"salt"`
			}
			if err := json.Unmarshal(body, &v); err != nil {
				t.Fatal(err)
			}
			plain, err := cipher.Open(v.Payload, "blob:"+string(hash))
			if err != nil || string(plain) != "source code" {
				t.Fatalf("blob: %q, %v", plain, err)
			}
			if v.Salt != base64.StdEncoding.EncodeToString(cipher.Salt) {
				t.Fatal("missing salt")
			}
			return response(`{}`), nil
		case "/v1/sync/rows":
			rowUploads++
			var v struct {
				Rows []Row `json:"rows"`
			}
			if err := json.Unmarshal(body, &v); err != nil {
				t.Fatal(err)
			}
			if len(v.Rows) != 1 || len(v.Rows[0].Data) != 0 {
				t.Fatal("row not encrypted")
			}
			plain, err := cipher.Open(v.Rows[0].Ciphertext, v.Rows[0].Table+":"+v.Rows[0].ID)
			if err != nil || !(bytes.Contains(plain, []byte("codex")) || bytes.Contains(plain, []byte(string(hash)))) {
				t.Fatalf("row: %q, %v", plain, err)
			}
			return response(`{}`), nil
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
			return nil, nil
		}
	})}
	_, rows, err := client.Sync(context.Background())
	if err != nil || rows != 2 {
		t.Fatalf("first sync: %d, %v", rows, err)
	}
	_, rows, err = client.Sync(context.Background())
	if err != nil || rows != 0 {
		t.Fatalf("second sync: %d, %v", rows, err)
	}
	if rowUploads != 2 || blobUploads != 2 {
		t.Fatalf("uploads: rows=%d blobs=%d", rowUploads, blobUploads)
	}
}

func TestSyncRequiresLoginAndTLS(t *testing.T) {
	c := &Client{Config: Config{Endpoint: "http://sync.example", Mode: "team"}}
	if _, _, err := c.Sync(context.Background()); err == nil {
		t.Fatal("HTTP endpoint accepted")
	}
	c.Config.Endpoint = "https://sync.example"
	if _, _, err := c.Sync(context.Background()); err == nil {
		t.Fatal("missing login accepted")
	}
}

func TestSyncRetriesUnacknowledgedRows(t *testing.T) {
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
	if _, err := db.Exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('01JRETRY','codex','repo',1)"); err != nil {
		t.Fatal(err)
	}
	requests := 0
	c := &Client{Config: Config{Endpoint: "https://sync.example", Remote: "owner/repo", Mode: "team"}, Token: "token", TraceDir: dir, DB: db.DB, Blobs: bs}
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(body, []byte("codex")) {
			t.Fatal("team row missing plaintext")
		}
		if requests == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return response(`{}`), nil
	})}
	if _, _, err := c.Sync(context.Background()); err == nil {
		t.Fatal("server failure accepted")
	}
	_, rows, err := c.Sync(context.Background())
	if err != nil || rows != 1 || requests != 2 {
		t.Fatalf("retry: rows=%d requests=%d err=%v", rows, requests, err)
	}
}

func TestBlobHaveBatches(t *testing.T) {
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
	for i := 0; i < 501; i++ {
		hash, err := bs.PutBytes([]byte(fmt.Sprintf("blob %d", i)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO ops(id,path,kind,after_hash,source,at) VALUES(?,?,?,?,'watcher',?)", fmt.Sprintf("01J%06d", i), fmt.Sprintf("file-%d", i), "add", hash, i); err != nil {
			t.Fatal(err)
		}
	}
	var counts []int
	c := &Client{Config: Config{Endpoint: "https://sync.example", Remote: "owner/repo", Mode: "team"}, Token: "token", TraceDir: dir, DB: db.DB, Blobs: bs}
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/sync/blobs/have" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		var v struct {
			Mode   string   `json:"mode"`
			Hashes []string `json:"hashes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Fatal(err)
		}
		if v.Mode != "team" {
			t.Fatalf("have mode = %q, want team", v.Mode)
		}
		counts = append(counts, len(v.Hashes))
		return response(`{"need":[]}`), nil
	})}
	if _, err := c.syncBlobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(counts) != 2 || counts[0] != 500 || counts[1] != 1 {
		t.Fatalf("batch sizes: %v", counts)
	}
}
