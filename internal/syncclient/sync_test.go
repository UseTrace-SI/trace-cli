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
