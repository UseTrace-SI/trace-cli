package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
)

// fakeCredentialStore puts a secret-tool stand-in alone on PATH. It keeps
// entries as files in a temporary directory, so tests never reach the user's
// Secret Service or Keychain.
func fakeCredentialStore(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the credential store fake implements secret-tool only")
	}
	bin := t.TempDir()
	for _, tool := range []string{"cat", "od", "tr"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("the credential store fake needs %s: %v", tool, err)
		}
		if err := os.Symlink(path, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	// Entries are named by the hex-encoded account, so "/" and "|" in
	// credential keys stay inside the store directory.
	script := `#!/bin/sh
set -e
case "$1" in
store) account=$4 ;;
lookup) account=$3 ;;
*) exit 2 ;;
esac
entry="$TRACE_TEST_CREDENTIALS/$(printf '%s' "$account" | od -An -tx1 | tr -d ' \n')"
if [ "$1" = store ]; then cat >"$entry"; else cat "$entry"; fi
`
	if err := os.WriteFile(filepath.Join(bin, "secret-tool"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_TEST_CREDENTIALS", t.TempDir())
	t.Setenv("PATH", bin)
	t.Setenv("TRACE_SYNC_PASSPHRASE", "")
}

// A legacy Pro/Team login is a GitHub token sent as a bearer token. Logging in
// to a second Trace server for the same repository must not change what the
// first server receives, and a .trace/login.json naming another server must
// not reach either login.
func TestGitHubLoginStaysWithItsServer(t *testing.T) {
	fakeCredentialStore(t)
	const remote = "acme/app"
	// A login saved by an older version under the repository name alone.
	if err := syncclient.SaveCredentials(remote, syncclient.Credentials{AccessToken: "token-saved-by-older-version"}); err != nil {
		t.Fatal(err)
	}
	logins := []struct {
		cfg        syncclient.Config
		passphrase string
		traceDir   string
	}{
		{syncclient.Config{Endpoint: "https://usetrace.si", Remote: remote, Mode: "pro", AutoSync: true}, "hosted server passphrase", t.TempDir()},
		{syncclient.Config{Endpoint: "https://trace.corp.example/", Remote: remote, Mode: "pro", AutoSync: true}, "corporate server passphrase", t.TempDir()},
	}
	for _, login := range logins {
		t.Setenv("TRACE_SYNC_PASSPHRASE", login.passphrase)
		if err := saveGitHubLogin(login.traceDir, login.cfg, syncclient.Credentials{AccessToken: "token-for-" + login.cfg.Endpoint}); err != nil {
			t.Fatal(err)
		}
	}
	// Background sync runs without the passphrase.
	t.Setenv("TRACE_SYNC_PASSPHRASE", "")
	for _, login := range logins {
		t.Run(login.cfg.Endpoint, func(t *testing.T) {
			provider, err := syncTokenProvider(login.cfg)
			if err != nil {
				t.Fatal(err)
			}
			token, err := provider(context.Background())
			if err != nil || token != "token-for-"+login.cfg.Endpoint {
				t.Fatalf("sync to %s would send Authorization: Bearer %s (err %v)", login.cfg.Endpoint, token, err)
			}
			cipher, err := configuredCipher(login.traceDir, login.cfg)
			if err != nil {
				t.Fatal(err)
			}
			sealed, err := cipher.Seal([]byte(`{"id":"T1"}`), "turns:T1")
			if err != nil {
				t.Fatal(err)
			}
			recovery, err := syncclient.Derive(login.passphrase, cipher.Salt)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := recovery.Open(sealed, "turns:T1"); err != nil {
				t.Fatalf("rows uploaded to %s are sealed with another login's key: %v", login.cfg.Endpoint, err)
			}
		})
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	planted := `{"endpoint":"https://attacker.example","remote":"acme/app","mode":"team","auto_sync":true}`
	if err := os.WriteFile(loginPath(root), []byte(planted), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := readSyncConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if provider, err := syncTokenProvider(cfg); err == nil {
		token, _ := provider(context.Background())
		t.Fatalf("repository-supplied login.json sends Authorization: Bearer %s to %s", token, cfg.Endpoint)
	}
}
