package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

func TestUploadPreviewIsLocalAndDoesNotRevealCredentials(t *testing.T) {
	root := competitiveRepo(t)
	secret := "sk-abcdefghijklmnopqrstuvwxyz1234567890"
	if err := os.WriteFile(filepath.Join(root, "config.txt"), []byte("api_key="+secret), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	e.Close()
	if err := os.WriteFile(filepath.Join(root, ".traceignore"), []byte("config.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printUploadPreview(context.Background(), root, &out, true); err != nil {
		t.Fatal(err)
	}
	var got uploadPreview
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.PotentialSecrets) != 1 || len(got.ExcludedHistory) != 1 || strings.Contains(out.String(), secret) {
		t.Fatalf("unsafe or incomplete preview: %s", out.String())
	}
	// A dry run works before login and does not initiate a credential operation.
	cmd := syncCommand(&root, new(bool))
	cmd.SetArgs([]string{"--dry-run"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

// Historical snapshots of built-in exclusions are never uploaded, so the
// preview lists them as left out and neither counts nor scans them; paths
// .traceignore excludes are still uploaded and still scanned.
func TestUploadPreviewSeparatesLeftOutHistory(t *testing.T) {
	root := competitiveRepo(t)
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.Blobs.PutBytes([]byte("package app\n"))
	if err != nil {
		t.Fatal(err)
	}
	npmrc, err := e.Blobs.PutBytes([]byte("//registry.example/:_authToken=sk-abcdefghijklmnopqrstuvwxyz1234567890\n"))
	if err != nil {
		t.Fatal(err)
	}
	ignored, err := e.Blobs.PutBytes([]byte("notes\n"))
	if err != nil {
		t.Fatal(err)
	}
	mh, err := (manifest.Manifest{"app.go": {Hash: app, Mode: 0644}, ".npmrc": {Hash: npmrc, Mode: 0644}, "notes.txt": {Hash: ignored, Mode: 0644}}).Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) VALUES('k',1,?,'turn',1)", mh); err != nil {
		t.Fatal(err)
	}
	e.Close()
	if err := os.WriteFile(filepath.Join(root, ".traceignore"), []byte("notes.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printUploadPreview(context.Background(), root, &out, true); err != nil {
		t.Fatal(err)
	}
	var got uploadPreview
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.LeftOut, ",") != ".npmrc" || strings.Join(got.StillUploaded, ",") != "notes.txt" || strings.Join(got.ExcludedHistory, ",") != ".npmrc,notes.txt" {
		t.Fatalf("historical paths: left out %v, still uploaded %v, all %v", got.LeftOut, got.StillUploaded, got.ExcludedHistory)
	}
	if got.Files != 2 || got.Objects != 2 || len(got.PotentialSecrets) != 0 {
		t.Fatalf("left-out history counted or scanned: %+v", got)
	}
}
