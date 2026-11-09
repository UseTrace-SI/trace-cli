package cli

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/plugins"
	"github.com/spf13/cobra"
)

func TestManagedPluginCLIRequiresExplicitTrustAndPreservesLiteralArguments(t *testing.T) {
	t.Setenv("TRACE_HOME", filepath.Join(t.TempDir(), "trace-user"))
	bundle := t.TempDir()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "publisher.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	code := []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\"\n")
	manifest := plugins.Manifest{Format: 1, Name: "example", Publisher: "publisher", Version: "1.0.0", OS: runtime.GOOS, Arch: runtime.GOARCH, SHA256: fmt.Sprintf("%x", sha256.Sum256(code)), Bytes: len(code)}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"manifest.json": raw, "manifest.sig": []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, raw))), "plugin": code} {
		if err := os.WriteFile(filepath.Join(bundle, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	execute := func(args ...string) (string, error) {
		var output bytes.Buffer
		repo := ""
		jsonOutput := true
		root := &cobra.Command{Use: "trace", SilenceErrors: true, SilenceUsage: true}
		root.PersistentFlags().StringVar(&repo, "repo", "", "repo")
		root.AddCommand(pluginManagerCommand(&repo, &jsonOutput))
		root.SetArgs(args)
		root.SetOut(&output)
		root.SetErr(&output)
		err := root.Execute()
		return output.String(), err
	}
	if _, err := execute("plugin", "install", bundle); err == nil {
		t.Fatal("installed without explicit publisher trust")
	}
	if _, err := execute("plugin", "trust", "--publisher", "publisher", "--key", keyPath); err != nil {
		t.Fatal(err)
	}
	if out, err := execute("plugin", "install", bundle); err != nil || !strings.Contains(out, `"executed":false`) {
		t.Fatalf("install: %s %v", out, err)
	}
	repo := filepath.Join(t.TempDir(), "repo with spaces")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	out, err := execute("--repo", repo, "plugin", "run", "example", "--", "--repo=literal", "$(not-a-command)")
	if err != nil || out != canonical+"\n--repo=literal\n$(not-a-command)\n" {
		t.Fatalf("passthrough: %q %v", out, err)
	}
	if _, err := execute("plugin", "untrust", "publisher"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute("plugin", "run", "example"); err == nil {
		t.Fatal("revoked publisher plugin executed")
	}
}
