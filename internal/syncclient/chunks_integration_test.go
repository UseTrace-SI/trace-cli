//go:build syncintegration

package syncclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

func TestRegressionLargeBlobWorkerRoundTrip(t *testing.T) {
	worker := startWorkerBridge(t)
	for _, mode := range []string{"team", "pro"} {
		t.Run(mode, func(t *testing.T) {
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
			// Over the old server limit, with varying bytes and reusable chunks.
			data := bytes.Repeat([]byte("a"), 8*1024*1024+1024)
			copy(data[:128], bytes.Repeat([]byte("b"), 128))
			copy(data[len(data)-128:], bytes.Repeat([]byte("c"), 128))
			h, err := bs.Put(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			m := manifest.Manifest{"large.bin": {Hash: h, Mode: 0600}}
			mh, err := m.Save(bs)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) VALUES('BASELINE',0,?,'baseline',1)", mh); err != nil {
				t.Fatal(err)
			}
			client := &Client{HTTP: worker, Config: Config{Endpoint: "https://usetrace.si", Remote: "octocat/large-" + mode, Mode: mode}, Token: "valid_token", TraceDir: dir, DB: db.DB, Blobs: bs}
			passphrase := "regression passphrase long enough"
			if mode == "pro" {
				client.Cipher, err = Derive(passphrase, bytes.Repeat([]byte{7}, 32))
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := client.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			if n, rows, err := client.Sync(context.Background()); err != nil || n != 0 || rows != 0 {
				t.Fatalf("repeat sync: blobs=%d rows=%d err=%v", n, rows, err)
			}
			recovery := filepath.Join(t.TempDir(), ".trace")
			if _, _, err := client.Pull(context.Background(), recovery, passphrase); err != nil {
				t.Fatal(err)
			}
			recovered, err := blob.Open(filepath.Join(recovery, "objects"))
			if err != nil {
				t.Fatal(err)
			}
			r, err := recovered.Get(h)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			sum := sha256.New()
			n, err := io.Copy(sum, r)
			if err != nil {
				t.Fatal(err)
			}
			if n != int64(len(data)) || fmt.Sprintf("%x", sum.Sum(nil)) != string(h) {
				t.Fatal("large blob changed during recovery")
			}
		})
	}
}
