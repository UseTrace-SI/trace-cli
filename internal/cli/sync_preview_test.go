package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
