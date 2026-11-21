package syncclient

import (
	"errors"
	"strings"
	"testing"
)

// Pro/Team credentials are GitHub tokens sent as bearer tokens, so each Trace
// server reads only the login saved for it. An entry saved by an older version
// under the repository name alone records no server, and .trace/login.json may
// come from the repository itself, so that entry is never migrated.
func TestGitHubCredentialsAreScopedToTheirServer(t *testing.T) {
	const remote = "acme/app"
	for _, mode := range []string{"pro", "team"} {
		hosted := Config{Endpoint: "https://usetrace.si", Remote: remote, Mode: mode}
		corp := Config{Endpoint: "https://trace.corp.example/", Remote: remote, Mode: mode}
		planted := Config{Endpoint: "https://attacker.example", Remote: remote, Mode: mode, AutoSync: true}
		twoServers := func() map[string]Credentials {
			return map[string]Credentials{hosted.CredentialKey(): {AccessToken: "hosted-token"}, corp.CredentialKey(): {AccessToken: "corp-token"}}
		}
		for _, tc := range []struct {
			name  string
			saved map[string]Credentials
			load  Config
			want  string // empty: no token may be returned or migrated
		}{
			{"hosted server reads its own login", twoServers(), hosted, "hosted-token"},
			{"corporate server reads its own login", twoServers(), corp, "corp-token"},
			{"repository-supplied config names another server", map[string]Credentials{remote: {AccessToken: "token-saved-by-older-version"}, hosted.CredentialKey(): {AccessToken: "hosted-token"}}, planted, ""},
			{"login saved by an older version", map[string]Credentials{remote: {AccessToken: "token-saved-by-older-version"}}, hosted, ""},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				read := func(key string) (Credentials, error) {
					if c, ok := tc.saved[key]; ok {
						return c, nil
					}
					return Credentials{}, errors.New("missing")
				}
				save := func(key string, c Credentials) error { tc.saved[key] = c; return nil }
				got, err := loadConfigCredentials(tc.load, read, save)
				if tc.want != "" {
					if err != nil || got.AccessToken != tc.want {
						t.Fatalf("got %q, %v; want %q", got.AccessToken, err, tc.want)
					}
					return
				}
				if err == nil || got.AccessToken != "" || !strings.Contains(err.Error(), "trace login") {
					t.Fatalf("%s would receive %q (err %v); want a re-login error", tc.load.Endpoint, got.AccessToken, err)
				}
				if _, migrated := tc.saved[tc.load.CredentialKey()]; migrated {
					t.Fatal("unverified login migrated to the configured server")
				}
			})
		}
	}
}

func TestPersonalCredentialIsolationAndMigration(t *testing.T) {
	a := Config{Endpoint: "https://trace.example", Remote: "solo/project", Mode: "personal", ConnectionID: "alice"}
	b := a
	b.ConnectionID = "bob"
	if a.CredentialKey() == b.CredentialKey() {
		t.Fatal("distinct connections share a credential entry")
	}
	entries := map[string]Credentials{a.legacyCredentialKey(): {ConnectionID: "bob", AccessToken: "bob-private-token"}}
	read := func(key string) (Credentials, error) {
		if c, ok := entries[key]; ok {
			return c, nil
		}
		return Credentials{}, errors.New("missing")
	}
	save := func(key string, c Credentials) error { entries[key] = c; return nil }
	if _, err := loadConfigCredentials(a, read, save); err == nil {
		t.Fatal("Alice adopted Bob's legacy credentials")
	}
	if _, err := loadConfigCredentials(b, read, save); err != nil {
		t.Fatal(err)
	}
	if entries[b.CredentialKey()].ConnectionID != "bob" {
		t.Fatal("matching entry was not migrated")
	}
	entries[a.CredentialKey()] = Credentials{ConnectionID: "bob", AccessToken: "wrong"}
	if _, err := loadConfigCredentials(a, read, save); err == nil {
		t.Fatal("mismatched scoped credential accepted")
	}
	a.ConnectionID = ""
	if _, err := loadConfigCredentials(a, read, save); err == nil {
		t.Fatal("missing identity accepted")
	}
}
