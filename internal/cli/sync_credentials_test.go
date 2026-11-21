package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
)

// recoverable seals a row with cipher and opens it the way trace sync pull
// does: from the passphrase and the salt uploaded with the row.
func recoverable(cipher *syncclient.Cipher, passphrase string) error {
	sealed, err := cipher.Seal([]byte(`{"id":"T1","prompt":"history"}`), "turns:T1")
	if err != nil {
		return err
	}
	recovery, err := syncclient.Derive(passphrase, cipher.Salt)
	if err != nil {
		return err
	}
	_, err = recovery.Open(sealed, "turns:T1")
	return err
}

// Two checkouts of one repository log in with automatic Pro sync and share one
// saved login. The checkout whose key was replaced must refuse to seal rows with
// the other key under its own salt, which recovery could never open, and its
// daemon must report that in trace sync status.
func TestProKeyStaysWithItsCheckoutSalt(t *testing.T) {
	t.Setenv("TRACE_HOME", t.TempDir())
	rootA := competitiveRepo(t)
	e, err := engine.Open(rootA)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	fakeCredentialStore(t)
	const passphrase = "a sufficiently long passphrase"
	// A loopback endpoint: nothing here may reach the network.
	cfg := syncclient.Config{Endpoint: "https://127.0.0.1:1", Remote: "acme/app", Mode: "pro", AutoSync: true}
	traceA, traceB := repo.TraceDir(rootA), t.TempDir()
	t.Setenv("TRACE_SYNC_PASSPHRASE", passphrase)
	for _, dir := range []string{traceA, traceB} {
		if err := saveGitHubLogin(dir, cfg, syncclient.Credentials{AccessToken: "github-token"}); err != nil {
			t.Fatal(err)
		}
	}
	// Background sync runs without the passphrase.
	t.Setenv("TRACE_SYNC_PASSPHRASE", "")
	cipher, err := configuredCipher(traceB, cfg)
	if err != nil {
		t.Fatalf("checkout that logged in last: %v", err)
	}
	if err := recoverable(cipher, passphrase); err != nil {
		t.Fatalf("checkout that logged in last uploads unrecoverable rows: %v", err)
	}
	cipher, err = configuredCipher(traceA, cfg)
	if err == nil {
		t.Fatalf("checkout A seals rows with checkout B's key (recovery: %v)", recoverable(cipher, passphrase))
	}
	if !strings.Contains(err.Error(), "trace login") {
		t.Fatalf("refusal does not say how to recover: %v", err)
	}
	if err := writeSyncConfig(loginPath(rootA), cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		autoSyncLoop(ctx, rootA, e, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	}()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		health, err := syncclient.ReadHealth(traceA)
		if err == nil && strings.Contains(health.LastError, "trace login") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon refusal missing from sync status: %+v %v", health, err)
		}
	}
}

// Renewing an expiring GitHub App token must keep the saved Pro key usable.
func TestProKeySurvivesTokenRefresh(t *testing.T) {
	fakeCredentialStore(t)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/cli/refresh" {
			http.NotFound(w, r)
			return
		}
		later := time.Now().Add(time.Hour).UnixMilli()
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "renewed-token", "refresh_token": "renewed-refresh", "expires_at": later, "refresh_expires_at": later})
	}))
	defer platform.Close()
	const passphrase = "a sufficiently long passphrase"
	cfg := syncclient.Config{Endpoint: platform.URL, Remote: "acme/app", Mode: "pro", AutoSync: true, GitHubApp: true, BrokerRefresh: true}
	traceDir := t.TempDir()
	t.Setenv("TRACE_SYNC_PASSPHRASE", passphrase)
	expired := syncclient.Credentials{AccessToken: "expired-token", RefreshToken: "refresh", ExpiresAt: 1, RefreshExpiresAt: time.Now().Add(time.Hour).UnixMilli()}
	if err := saveGitHubLogin(traceDir, cfg, expired); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_SYNC_PASSPHRASE", "")
	provider, err := syncTokenProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if token, err := provider(context.Background()); err != nil || token != "renewed-token" {
		t.Fatalf("token refresh: %q, %v", token, err)
	}
	cipher, err := configuredCipher(traceDir, cfg)
	if err != nil {
		t.Fatalf("Pro key lost after token refresh: %v", err)
	}
	if err := recoverable(cipher, passphrase); err != nil {
		t.Fatal(err)
	}
}

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
