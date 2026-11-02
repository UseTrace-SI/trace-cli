package syncclient

import (
	"errors"
	"testing"
)

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
