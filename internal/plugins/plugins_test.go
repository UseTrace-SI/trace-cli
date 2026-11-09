package plugins

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testManager(t *testing.T) (Manager, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	temporary, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(temporary, "state with spaces")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager := Manager{Root: filepath.Join(parent, "plugins")}
	if _, err := manager.Trust("publisher", public, false); err != nil {
		t.Fatal(err)
	}
	return manager, public, private
}
func signedBundle(t *testing.T, key ed25519.PrivateKey, publisher, version, body string) string {
	t.Helper()
	path := t.TempDir()
	manifest := Manifest{Format: 1, Name: "example", Publisher: publisher, Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH, SHA256: digest([]byte(body)), Bytes: len(body)}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"manifest.json": raw, "manifest.sig": []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, raw))), "plugin": []byte(body)} {
		if err := os.WriteFile(filepath.Join(path, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
func TestSignedOfflineInstallUpdatesDowngradesAndPrunesOnlyOwnedReleases(t *testing.T) {
	manager, _, key := testManager(t)
	one := signedBundle(t, key, "publisher", "1.0.0", "#!/bin/sh\nprintf '%s\\n' \"$@\"\n")
	receipt, err := manager.Install(one, false)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Manifest.Version != "1.0.0" {
		t.Fatal(receipt)
	}
	executable, err := manager.Executable("example")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable, "literal $(no-command)", "--argument").CombinedOutput()
	if err != nil || string(output) != "literal $(no-command)\n--argument\n" {
		t.Fatalf("literal plugin arguments failed: %s %v", output, err)
	}
	two := signedBundle(t, key, "publisher", "2.0.0", "#!/bin/sh\necho two\n")
	if _, err := manager.Install(two, false); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(one, false); err == nil {
		t.Fatal("silent downgrade accepted")
	}
	if _, err := manager.Install(one, true); err != nil {
		t.Fatal(err)
	}
	altered := signedBundle(t, key, "publisher", "1.0.0", "#!/bin/sh\necho mutable\n")
	if _, err := manager.Install(altered, true); err == nil {
		t.Fatal("same version changed signed bytes")
	}
	three := signedBundle(t, key, "publisher", "3.0.0", "#!/bin/sh\necho three\n")
	if _, err := manager.Install(three, false); err != nil {
		t.Fatal(err)
	}
	count, err := manager.Prune("example")
	if err != nil || count != 1 {
		t.Fatalf("active/previous retention: %d %v", count, err)
	}
	items, err := manager.List()
	if err != nil || len(items) != 1 || !items[0].Verified || items[0].Receipt.Manifest.Version != "3.0.0" {
		t.Fatalf("verification list: %+v %v", items, err)
	}
	if err := manager.Remove("example"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Executable("example"); err == nil {
		t.Fatal("deactivated plugin ran")
	}
	if count, err := manager.Prune("example"); err != nil || count != 2 {
		t.Fatalf("inactive cleanup: %d %v", count, err)
	}
}
func TestTamperingWrongPublishersAndKeyRevocationFailClosed(t *testing.T) {
	manager, _, key := testManager(t)
	bundle := signedBundle(t, key, "publisher", "1.0.0", "#!/bin/sh\necho verified\n")
	if _, err := manager.Install(bundle, false); err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrong := signedBundle(t, private, "publisher", "2.0.0", "not executed")
	if _, err := manager.Install(wrong, false); err == nil {
		t.Fatal("untrusted signature installed")
	}
	if _, err := manager.Trust("other", public, false); err != nil {
		t.Fatal(err)
	}
	other := signedBundle(t, private, "other", "2.0.0", "not executed")
	if _, err := manager.Install(other, false); err == nil {
		t.Fatal("publisher changed during update")
	}
	if _, err := manager.Trust("publisher", public, false); err == nil {
		t.Fatal("key replaced without explicit revocation")
	}
	if _, err := manager.Trust("publisher", public, true); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Executable("example"); err == nil {
		t.Fatal("old publisher key remained valid")
	}
	rotated := signedBundle(t, private, "publisher", "1.0.0", "#!/bin/sh\necho verified\n")
	if _, err := manager.Install(rotated, false); err != nil {
		t.Fatal("could not reinstall identical bytes under newly pinned key:", err)
	}
	path, err := manager.Executable("example")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0500); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Executable("example"); err == nil {
		t.Fatal("changed installed binary ran")
	}
	if err := manager.Untrust("publisher"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Executable("example"); err == nil {
		t.Fatal("untrusted plugin ran")
	}
}
func TestManifestAmbiguitySymlinksAndUnexpectedFilesAreRejected(t *testing.T) {
	manager, _, key := testManager(t)
	bundle := signedBundle(t, key, "publisher", "1.0.0", "plugin bytes")
	raw, err := os.ReadFile(filepath.Join(bundle, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	duplicate := []byte(strings.Replace(string(raw), `"format":1`, `"format":1,"format":1`, 1))
	if _, err := parseManifest(duplicate); err == nil {
		t.Fatal("duplicate manifest keys accepted")
	}
	if err := os.Rename(filepath.Join(bundle, "plugin"), filepath.Join(bundle, "actual")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("actual", filepath.Join(bundle, "plugin")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(bundle, false); err == nil {
		t.Fatal("bundle symlink accepted")
	}
	valid := signedBundle(t, key, "publisher", "1.0.0", "one")
	if _, err := manager.Install(valid, false); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"2.0.0", "3.0.0"} {
		if _, err := manager.Install(signedBundle(t, key, "publisher", version, version), false); err != nil {
			t.Fatal(err)
		}
	}
	first, err := parseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	first.SHA256 = digest([]byte("one"))
	first.Bytes = 3
	trustedKey, err := manager.readKey("publisher")
	if err != nil {
		t.Fatal(err)
	}
	unexpected := filepath.Join(releasePath(manager.Root, Receipt{Manifest: first, PublisherFingerprint: digest(trustedKey)}), "user-file")
	if err := os.WriteFile(unexpected, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Prune("example"); err == nil {
		t.Fatal("pruned unexpected content")
	}
	if data, err := os.ReadFile(unexpected); err != nil || string(data) != "preserve" {
		t.Fatal("unrelated file was removed")
	}
}
func TestTrustInputAcceptsPublicPEMAndRejectsPrivateKeyMaterial(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	key, err := DecodePublicKey(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	if err != nil || !public.Equal(key) {
		t.Fatal("public key not decoded")
	}
	secret, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePublicKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: secret})); err == nil {
		t.Fatal("private key material accepted")
	}
}
