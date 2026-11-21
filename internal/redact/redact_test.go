package redact

import (
	"strings"
	"testing"
)

func TestKnownSecrets(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"aws", "AKIAABCDEFGHIJKLMNOP"},
		{"github", "ghp_abcdefghijklmnopqrstuvwxyz012345"},
		{"openai", "sk-abcdefghijklmnopqrstuvwxyz012345"},
		{"env", "API_TOKEN=abcdefghijklmnopqrstuvwxyz012345"},
		{"unicode folded context", "ſecret value 7dF2aQ9mR4xT8vL1pN6cY3zB5kH0sW2u"},
		{"private key", "-----BEGIN PRIVATE KEY-----\nmaterial\n-----END PRIVATE KEY-----"},
		{"slack", "xoxb-1234567890-abcdefghij"},
		{"stripe", "sk_live_abcdefghijklmnop123456"},
		{"jwt", "eyJabcdefghij.eyJabcdefghij.abcdefghijk"},
		{"URL password", "postgres://user:password@example.invalid/db"},
		{"entropy", "api value 7dF2aQ9mR4xT8vL1pN6cY3zB5kH0sW2u"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (Redactor{}).Text(tc.input)
			if strings.Contains(got, tc.input) || !strings.Contains(got, "⟨redacted:") {
				t.Fatalf("unredacted: %q", got)
			}
		})
	}
}

func TestPEMPrivateKeysRedacted(t *testing.T) {
	for _, tc := range []struct {
		name, block  string
		wantRedacted bool
	}{
		{"pkcs8", "PRIVATE KEY", true},
		{"rsa", "RSA PRIVATE KEY", true},
		{"ec", "EC PRIVATE KEY", true},
		{"openssh", "OPENSSH PRIVATE KEY", true},
		{"encrypted", "ENCRYPTED PRIVATE KEY", true},
		{"pgp", "PGP PRIVATE KEY BLOCK", true},
		{"public key", "PUBLIC KEY", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "-----BEGIN " + tc.block + "-----\nsecret-material\n-----END " + tc.block + "-----"
			got := (Redactor{}).Text(input)
			if tc.wantRedacted {
				if strings.Contains(got, "secret-material") || !strings.Contains(got, "⟨redacted:private-key:") {
					t.Fatalf("PEM block was not redacted: %q", got)
				}
			} else if got != input {
				t.Fatalf("false positive: %q", got)
			}
		})
	}
}

func TestShortHighEntropyKeyValuesRedacted(t *testing.T) {
	for _, tc := range []struct {
		name, input  string
		wantRedacted bool
	}{
		{"12 char secret in key context", "the key is aB3xK9mQ2zLp7", true},
		{"low entropy key value", "the key is aaaaaaaaaaaa", false},
		{"high entropy without key context", "value aB3xK9mQ2zLp7 here", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (Redactor{}).Text(tc.input)
			if tc.wantRedacted {
				if !strings.Contains(got, "⟨redacted:entropy:") || strings.Contains(got, "aB3xK9mQ2zLp7") {
					t.Fatalf("short secret was not redacted: %q", got)
				}
			} else if got != tc.input {
				t.Fatalf("false positive: %q -> %q", tc.input, got)
			}
		})
	}
}

// The value after any assignment or comparison operator must be masked whole.
// Matching only the operator's second character used to leave the real value
// in place, and that marker then suppressed the entropy fallback for it.
func TestAssignmentAndComparisonValuesRedacted(t *testing.T) {
	const hex = "3f9a8c2e71d64b0f9e5a2c7d18b4e6f0"
	env := func(value string) string { return marker("env", value) }
	for _, tc := range []struct{ name, input, want string }{
		{"go short variable declaration", `token := "` + hex + `"`, `token := "` + env(hex) + `"`},
		{"camelCase name", `apiKey := "9Qm2Lx7Rt4Vb8Nc1Zp6Hs3Wd5Kf0Jg2Y"`, `apiKey := "` + env("9Qm2Lx7Rt4Vb8Nc1Zp6Hs3Wd5Kf0Jg2Y") + `"`},
		{"hash rocket", `:password => "Summer2024!"`, `:password => "` + env("Summer2024!") + `"`},
		{"equality", `if token == "` + hex + `" {`, `if token == "` + env(hex) + `" {`},
		{"strict inequality", `if (secret !== 'Summer2024!') {`, `if (secret !== '` + env("Summer2024!") + `') {`},
		{"quoted name in prose", `log in with {"username": "admin", "password": "Summer2024!"}`, `log in with {"username": "admin", "password": "` + env("Summer2024!") + `"}`},
		{"quoted value with spaces", `DB_PASSWORD="correct horse battery staple"`, `DB_PASSWORD="` + env("correct horse battery staple") + `"`},
		{"single-quoted name and value", `{'api_key': 'open sesame'}`, `{'api_key': '` + env("open sesame") + `'}`},
		{"assignment inside a string literal", `exec("API_TOKEN=abc123")`, `exec("API_TOKEN=` + env("abc123") + `")`},
		{"empty comparison is unchanged", `if token != "" {`, `if token != "" {`},
		{"comparison with an identifier is unchanged", `if token != nil {`, `if token != nil {`},
		{"arrow function is unchanged", `keys.map(key => key.id)`, `keys.map(key => key.id)`},
		{"quoted name with null is unchanged", `{"password": null}`, `{"password": null}`},
		{"quoted name with a number is unchanged", `"max_tokens": 4096`, `"max_tokens": 4096`},
		{"quoted name with a list is unchanged", `"keywords": ["redact", "trace"]`, `"keywords": ["redact", "trace"]`},
		{"quoted name not ending in a secret word is unchanged", `{"token_type": "bearer"}`, `{"token_type": "bearer"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redactor := Redactor{}
			got := redactor.Text(tc.input)
			if got != tc.want {
				t.Fatalf("Text(%q) = %q; want %q", tc.input, got, tc.want)
			}
			if twice := redactor.Text(got); twice != got {
				t.Fatalf("redaction changed on a second pass: %q -> %q", got, twice)
			}
		})
	}
}

func TestLargeUnrelatedTextIsUnchanged(t *testing.T) {
	value := strings.Repeat("x", 1<<20)
	if got := (Redactor{}).Text(value); got != value {
		t.Fatal("unrelated large text changed")
	}
}

func TestNegativesAndPII(t *testing.T) {
	for _, value := range []string{"0123456789abcdef0123456789abcdef01234567", "550e8400-e29b-41d4-a716-446655440000", "ordinary request"} {
		if got := (Redactor{}).Text(value); got != value {
			t.Errorf("false positive: %q -> %q", value, got)
		}
	}
	if got := (Redactor{PII: true}).Text("contact test@example.com"); strings.Contains(got, "test@example.com") {
		t.Fatalf("email leaked: %q", got)
	}
}

func TestRedactionMarkersAreStable(t *testing.T) {
	for _, tc := range []struct {
		name, input, secret string
	}{
		{"known token", "turn ghp_abcdefghijklmnopqrstuvwxyz012345", "ghp_abcdefghijklmnopqrstuvwxyz012345"},
		{"environment value", "API_TOKEN=ordinarysecret", "ordinarysecret"},
		{"token as environment value", "API_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz012345", "ghp_abcdefghijklmnopqrstuvwxyz012345"},
		{"value after existing marker", marker("github-token", "example") + " API_TOKEN=ordinarysecret", "ordinarysecret"},
		{"marker inside environment value", "API_TOKEN=" + marker("github-token", "example") + "ordinarysecret", "ordinarysecret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redactor := Redactor{}
			once := redactor.Text(tc.input)
			if strings.Contains(once, tc.secret) || !strings.Contains(once, "⟨redacted:") {
				t.Fatalf("secret was not redacted: %q", once)
			}
			if twice := redactor.Text(once); twice != once {
				t.Fatalf("redaction changed on a second pass: %q -> %q", once, twice)
			}
		})
	}
}
