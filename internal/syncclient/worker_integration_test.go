//go:build syncintegration

package syncclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

func startWorkerBridge(t *testing.T) *http.Client {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "trace-platform", "scripts", "sync_bridge.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", script)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if stderr.Len() > 0 {
			t.Logf("Worker bridge: %s", stderr.String())
		}
	})
	type ready struct {
		line string
		err  error
	}
	reader := bufio.NewReader(stdout)
	result := make(chan ready, 1)
	go func() {
		line, err := reader.ReadString('\n')
		result <- ready{line, err}
	}()
	select {
	case r := <-result:
		if r.err != nil {
			t.Fatalf("starting Worker bridge: %v: %s", r.err, stderr.String())
		}
		var v struct {
			Ready bool `json:"ready"`
		}
		if err := json.Unmarshal([]byte(r.line), &v); err != nil || !v.Ready {
			t.Fatalf("invalid Worker bridge greeting %q: %v", r.line, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Worker bridge did not start")
	}
	var mu sync.Mutex
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body []byte
		if request.Body != nil {
			var err error
			body, err = io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
		}
		wire, err := json.Marshal(map[string]any{
			"method": request.Method, "path": request.URL.RequestURI(),
			"headers": request.Header, "body": base64.StdEncoding.EncodeToString(body),
		})
		if err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if _, err := stdin.Write(append(wire, '\n')); err != nil {
			return nil, err
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		var reply struct {
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		}
		if err := json.Unmarshal(line, &reply); err != nil {
			return nil, err
		}
		data, err := base64.StdEncoding.DecodeString(reply.Body)
		if err != nil {
			return nil, err
		}
		headers := make(http.Header)
		for key, value := range reply.Headers {
			headers.Set(key, value)
		}
		return &http.Response{StatusCode: reply.Status, Status: fmt.Sprintf("%d %s", reply.Status, http.StatusText(reply.Status)), Header: headers, Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
	})}
}

func TestClientUploadsToWorker(t *testing.T) {
	for _, mode := range []string{"team", "pro"} {
		t.Run(mode, func(t *testing.T) {
			worker := startWorkerBridge(t)
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
			fileHash, err := bs.PutBytes([]byte("source code\n"))
			if err != nil {
				t.Fatal(err)
			}
			m := manifest.Manifest{"main.go": {Hash: fileHash, Mode: 0600}}
			manifestHash, err := m.Save(bs)
			if err != nil {
				t.Fatal(err)
			}
			prefix := "01J" + strings.ToUpper(mode)
			if _, err := db.Exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES(?,?,?,?)", prefix+"SESSION", "codex", "repo", 1); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO turns(id,session_id,seq,prompt,started_at,ended_at) VALUES(?,?,?,?,?,?)", prefix+"TURN", prefix+"SESSION", 1, "a private prompt", 1, 2); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO keyframes(id,turn_id,seq,manifest_hash,tag,created_at) VALUES(?,?,?,?,?,?)", prefix+"KEYFRAME", prefix+"TURN", 1, manifestHash, "turn", 2); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO ops(id,turn_id,path,kind,after_hash,source,at) VALUES(?,?,?,?,?,?,?)", prefix+"OP", prefix+"TURN", "main.go", "add", fileHash, "tool", 2); err != nil {
				t.Fatal(err)
			}
			client := &Client{Config: Config{Endpoint: "https://sync.example", Remote: "octocat/example", Mode: mode}, Token: "valid_token", TraceDir: dir, DB: db.DB, Blobs: bs, HTTP: worker}
			if mode == "pro" {
				client.Cipher, err = Derive("a sufficiently long passphrase", bytes.Repeat([]byte{7}, 32))
				if err != nil {
					t.Fatal(err)
				}
			}
			blobs, rows, err := client.Sync(context.Background())
			if err != nil || blobs != 2 || rows != 4 {
				t.Fatalf("first sync: blobs=%d rows=%d err=%v", blobs, rows, err)
			}
			blobs, rows, err = client.Sync(context.Background())
			if err != nil || blobs != 0 || rows != 0 {
				t.Fatalf("repeat sync: blobs=%d rows=%d err=%v", blobs, rows, err)
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec("UPDATE sessions SET ended_at=? WHERE id=?", 3, prefix+"SESSION"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec("UPDATE turns SET prompt=?,reasoning=?,ended_at=? WHERE id=?", "final private prompt", "final reasoning", 3, prefix+"TURN"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec("UPDATE ops SET session_id=? WHERE id=?", prefix+"SESSION", prefix+"OP"); err != nil {
				t.Fatal(err)
			}
			for _, update := range []struct{ table, id string }{
				{"sessions", prefix + "SESSION"}, {"turns", prefix + "TURN"}, {"ops", prefix + "OP"},
			} {
				if err := store.RecordUpdate(tx, update.table, update.id); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			blobs, rows, err = client.Sync(context.Background())
			if err != nil || blobs != 0 || rows != 3 {
				t.Fatalf("correction sync: blobs=%d rows=%d err=%v", blobs, rows, err)
			}
			resp, err := worker.Get("https://sync.example/__inspect")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			var stored struct {
				Rows    []Row `json:"rows"`
				Blobs   int   `json:"blobs"`
				Objects int   `json:"objects"`
			}
			if err := json.Unmarshal(body, &stored); err != nil {
				t.Fatal(err)
			}
			if len(stored.Rows) != 7 || stored.Blobs != 2 || stored.Objects != 2 || bytes.Contains(body, []byte("private prompt")) {
				t.Fatalf("Worker did not store opaque, idempotent history: %s", body)
			}
			recoveredDir := filepath.Join(t.TempDir(), ".trace")
			passphrase := ""
			if mode == "pro" {
				passphrase = "a sufficiently long passphrase"
			}
			gotBlobs, gotRows, err := client.Pull(context.Background(), recoveredDir, passphrase)
			if err != nil || gotBlobs != 2 || gotRows != 7 {
				t.Fatalf("pull: blobs=%d rows=%d err=%v", gotBlobs, gotRows, err)
			}
			recovered, err := store.Open(filepath.Join(recoveredDir, "trace.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			var prompt string
			if err := recovered.QueryRow("SELECT prompt FROM turns WHERE id=?", prefix+"TURN").Scan(&prompt); err != nil || prompt != "final private prompt" {
				t.Fatalf("recovered prompt=%q err=%v", prompt, err)
			}
			var endedAt int64
			if err := recovered.QueryRow("SELECT ended_at FROM sessions WHERE id=?", prefix+"SESSION").Scan(&endedAt); err != nil || endedAt != 3 {
				t.Fatalf("recovered session end=%d err=%v", endedAt, err)
			}
			var opSession string
			if err := recovered.QueryRow("SELECT session_id FROM ops WHERE id=?", prefix+"OP").Scan(&opSession); err != nil || opSession != prefix+"SESSION" {
				t.Fatalf("recovered operation session=%q err=%v", opSession, err)
			}
			recoveredBlobs, err := blob.Open(filepath.Join(recoveredDir, "objects"))
			if err != nil {
				t.Fatal(err)
			}
			reader, err := recoveredBlobs.Get(fileHash)
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(reader)
			reader.Close()
			if err != nil || string(content) != "source code\n" {
				t.Fatalf("recovered blob=%q err=%v", content, err)
			}
			history, err := engine.Open(filepath.Dir(recoveredDir))
			if err != nil {
				t.Fatalf("opening recovered history: %v", err)
			}
			history.Close()
			if mode == "pro" {
				failedDir := filepath.Join(t.TempDir(), ".trace")
				if _, _, err := client.Pull(context.Background(), failedDir, "a different long passphrase"); err == nil {
					t.Fatal("wrong Pro passphrase recovered history")
				}
				if _, err := os.Stat(failedDir); !os.IsNotExist(err) {
					t.Fatalf("failed pull left partial history: %v", err)
				}
			}
		})
	}
}
