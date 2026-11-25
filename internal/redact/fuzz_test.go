package redact

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// fuzzSeeds are inputs that once crashed or leaked: escaped values running
// past their match, assignments inside skipped values, and escaped values
// ending in literal backslashes.
var fuzzSeeds = []string{
	`{\"api_key\": \"https://h/cb?access_token=abc123\"}`,
	`{\"secret\": \"key: value\"}`,
	`PASSWORD=\"abc TOKEN=def\"`,
	`{"text":"{\"password\": \"a\\\\\"}, then export API_KEY=sk_abc"}`,
	`{\"DB_PASSWORD\" != secret:=QE66TZx`,
	"export \\\\\"api_key\\\\\": token=QTQ9Zx\n; ",
	`  \"token\" := \secret:=Q6G7HZx`,
	`{\"password\": \"C:\\\\temp\\\\\"}, next`,
	`{\"password\": \"ab\\\"cdLEAKED\"}`,
	`export DB_PASSWORD=\$hunter\$two`,
	"AKIA" + fill("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", 16) + " aB3xK9mQ2zLp7wR4",
	"api value 7dF2aQ9mR4xT8vL1pN6cY3zB5kH0sW2u",
}

func FuzzText(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			return
		}
		once := (Redactor{}).Text(input)
		if utf8.ValidString(input) && !utf8.ValidString(once) {
			t.Fatalf("redaction broke UTF-8: %q -> %q", input, once)
		}
		if twice := (Redactor{}).Text(once); twice != once {
			t.Fatalf("not stable on its own output:\n in    %q\n once  %q\n twice %q", input, once, twice)
		}
	})
}

func TestFuzzSeedsDoNotPanicAndMaskSecrets(t *testing.T) {
	for _, tc := range []struct{ input, leak string }{
		{`{\"api_key\": \"https://h/cb?access_token=abc123\"}`, "abc123"},
		{`{\"secret\": \"key: value\"}`, "value"},
		{`PASSWORD=\"abc TOKEN=def\"`, "def"},
		{`{"text":"{\"password\": \"a\\\\\"}, then export API_KEY=sk_abc"}`, "sk_abc"},
		{`{\"DB_PASSWORD\" != secret:=QE66TZx`, "QE66TZx"},
		{"export \\\\\"api_key\\\\\": token=QTQ9Zx\n; ", "QTQ9Zx"},
		{`  \"token\" := \secret:=Q6G7HZx`, "Q6G7HZx"},
		{`{\"password\": \"C:\\\\temp\\\\\"}, next`, "temp"},
		// Found by differential fuzzing against the earlier redactor: a
		// value split by a newly masked assignment is still judged whole,
		// and context words inside a masked value still count.
		{"\\\\\\\"passwd=>\\QYszbQxkey=QGb3gAbmx/\\DB_PASSWORD\": \\Qaix4P62x, ", "QYszbQx"},
		{", \\\\\\\"DB_PASSWORD!=\\\\\"null; \"my_token_type!=\"QBjrBbp3ux'\t\\\\\"secret\":=\\\\\\\"QaCM011ux\\\\\" 'my_token_type\\\\\\\" = \\\\\"QsAwWPR0g0g2Kx\\\"access_token\\\" = \\\"QB9JY1hexset ", "QsAwWPR0g0g2Kx"},
	} {
		got := (Redactor{}).Text(tc.input)
		if strings.Contains(got, tc.leak) || !strings.Contains(got, "⟨redacted:") {
			t.Errorf("Text(%q) = %q leaks %q", tc.input, got, tc.leak)
		}
		if twice := (Redactor{}).Text(got); twice != got {
			t.Errorf("second pass changed %q -> %q", got, twice)
		}
	}
	// The value ends at the escaped closing quote after its literal
	// backslashes; what follows is kept.
	if got := (Redactor{}).Text(`{\"password\": \"C:\\\\temp\\\\\"}, next`); !strings.HasSuffix(got, `\"}, next`) {
		t.Fatalf("closing quote lost: %q", got)
	}
}

// A high-entropy value right after a credential marker is in key context; a
// candidate inside a marker is never masked again.
func TestEntropyNearMarkers(t *testing.T) {
	aws := "AKIA" + fill("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", 16)
	got := (Redactor{}).Text(aws + " wJalrXUtnFEMI7K9bPxRfiCY")
	if strings.Contains(got, "wJalrXUtnFEMI7K9bPxRfiCY") || !strings.Contains(got, "⟨redacted:entropy:") {
		t.Fatalf("secret after an access key kept: %q", got)
	}
	marked := marker("digitalocean-token", "x") + " " + marker("azure-client-secret", "y")
	if got := (Redactor{}).Text("token " + marked); got != "token "+marked {
		t.Fatalf("marker internals re-masked: %q", got)
	}
	plain := "user@example.invalid wJalrXUtnFEMI7K9bPxRfiCY"
	if got := (Redactor{}).Text(plain); got != plain {
		t.Fatalf("no key context, yet masked: %q", got)
	}
}
