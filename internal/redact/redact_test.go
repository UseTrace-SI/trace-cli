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
