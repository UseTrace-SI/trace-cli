package redact

import (
	"strings"
	"testing"
)

// fill returns n characters cycling through alphabet, so fixtures have the
// shape of a credential without being one.
func fill(alphabet string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(alphabet[i%len(alphabet)])
	}
	return b.String()
}

const (
	alnum = "aB3dE5fG7hJ9kL2mN4pQ6rS8tU0vW1xY"
	lower = "q7w2e9r4t1y8u3i6o5p0a"
	hexs  = "3f9a8c2e71d64b0f"
)

// Each provider's credential format must be masked wherever it appears in
// prose, code or configuration, with the provider named in the marker.
func TestProviderCredentialCorpus(t *testing.T) {
	for _, tc := range []struct{ name, kind, secret string }{
		{"Google API key", "google-api-key", "AIza" + fill("Sy9_-"+alnum, 35)},
		{"GitLab personal token", "gitlab-token", "glpat-" + fill(alnum, 20)},
		{"GitLab deploy token", "gitlab-token", "gldt-" + fill(alnum, 24)},
		{"GitLab runner token", "gitlab-token", "glrt-" + fill(alnum, 24)},
		{"npm token", "npm-token", "npm_" + fill(alnum, 36)},
		{"Hugging Face token", "huggingface-token", "hf_" + fill(alnum, 34)},
		{"Slack bot token", "slack-token", "xox" + "b-" + "123456789012-1234567890123-" + fill(alnum, 24)},
		{"Slack legacy token", "slack-token", "xox" + "o-" + fill(alnum, 24)},
		{"Slack app token", "slack-token", "xapp-1-" + "A01234ABCDE-1234567890123-" + fill(hexs, 64)},
		{"Slack webhook", "slack-webhook", "https://hooks.slack.com/services/T00000000/B00000000/" + fill(alnum, 24)},
		{"SendGrid key", "sendgrid-key", "SG." + fill(alnum, 22) + "." + fill(alnum, 43)},
		{"DigitalOcean personal token", "digitalocean-token", "dop_v1_" + fill(hexs, 64)},
		{"DigitalOcean OAuth token", "digitalocean-token", "doo_v1_" + fill(hexs, 64)},
		{"DigitalOcean refresh token", "digitalocean-token", "dor_v1_" + fill(hexs, 64)},
		{"Stripe live secret key", "stripe-key", "sk_" + "live_" + fill(alnum, 24)},
		{"Stripe live restricted key", "stripe-key", "rk_" + "live_" + fill(alnum, 24)},
		{"Stripe webhook secret", "stripe-key", "whsec_" + fill(alnum, 32)},
		{"Anthropic key", "api-key", "sk-ant-api03-" + fill(alnum, 40)},
		{"OpenAI project key", "api-key", "sk-proj-" + fill(alnum, 48)},
		{"OpenAI legacy key", "api-key", "sk-" + fill(alnum, 48)},
		{"Twilio API key", "twilio-key", "SK" + fill(hexs, 32)},
		{"Shopify access token", "shopify-token", "shpat_" + fill(hexs, 32)},
		{"Shopify shared secret", "shopify-token", "shpss_" + fill(hexs, 32)},
		{"PyPI token", "pypi-token", "pypi-AgEIcHlwaS5vcmc" + fill(alnum, 60)},
		{"Azure AD client secret", "azure-client-secret", "abc8Q~" + fill(alnum, 34)},
		{"Atlassian API token", "atlassian-token", "ATATT3" + fill(alnum, 186)},
		{"GitHub token", "github-token", "ghp_" + fill(alnum, 36)},
		{"AWS access key", "aws-key", "AKIA" + fill("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", 16)},
		{"RSA private key", "private-key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----"},
		{"OpenSSH private key", "private-key", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blbn\n-----END OPENSSH PRIVATE KEY-----"},
	} {
		for _, context := range []struct{ name, before, after string }{
			{"bare", "", ""},
			{"prose", "please use ", " for the deploy"},
			{"code", `client := New("`, `")`},
			{"yaml", "value: ", "\nnext: 1"},
		} {
			t.Run(tc.name+"/"+context.name, func(t *testing.T) {
				input := context.before + tc.secret + context.after
				got := (Redactor{}).Text(input)
				if strings.Contains(got, tc.secret) {
					t.Fatalf("secret leaked: %q", got)
				}
				if !strings.Contains(got, "⟨redacted:"+tc.kind+":") {
					t.Fatalf("want a %s marker: %q", tc.kind, got)
				}
				if !strings.HasPrefix(got, context.before) || !strings.HasSuffix(got, context.after) {
					t.Fatalf("surrounding text changed: %q", got)
				}
				if twice := (Redactor{}).Text(got); twice != got {
					t.Fatalf("redaction changed on a second pass: %q -> %q", got, twice)
				}
			})
		}
	}
}

// Ordinary source, hashes and identifiers near the new prefixes must pass
// through unchanged.
func TestProviderPatternsAvoidFalsePositives(t *testing.T) {
	for _, input := range []string{
		"commit 0123456789abcdef0123456789abcdef01234567",
		"sha256:" + fill("0123456789abcdef", 64),
		"id 550e8400-e29b-41d4-a716-446655440000",
		`model = hf_hub_download(repo_id="bert-base-uncased", filename="config.json")`,
		"npm_config_registry=https://registry.npmjs.org/",
		"export npm_package_version=1.2.3",
		`const SG = "SG.example"`,
		"func (s *Shop) SKU() string { return s.sku }",
		"SKIP_SLOW_TESTS=1 go test ./...",
		"AIzaSyXXXX is a placeholder",
		"the glpat- prefix marks GitLab tokens",
		"see https://hooks.slack.com/ for webhooks",
		"do_v1_handler(ctx)",
		"shpat_ prefixed tokens are Shopify admin tokens",
		"pip install pypi-simple",
		"ATATT3 tokens are Atlassian API tokens",
		"Q~ is not a secret",
		"xapp-config.json",
		"task-runner and risk-assessment are ordinary words",
		`whsec_ is the Stripe webhook prefix`,
		"img data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
		"version v1.26.0 released 2026-11-25",
		"for i := 0; i < len(items); i++ { total += items[i].Count }",
	} {
		if got := (Redactor{}).Text(input); got != input {
			t.Errorf("false positive: %q -> %q", input, got)
		}
	}
}

// A pattern without literal hints is never tried at all.
func TestEveryPatternHasHints(t *testing.T) {
	for _, p := range patterns {
		if len(patternHints[p.kind]) == 0 {
			t.Errorf("pattern %s has no hints and never runs", p.kind)
		}
	}
}

// A credential glued to a counter or label by a digit or underscore, where
// \b finds no word boundary, must still be masked; the glue is kept.
func TestCredentialsAfterDigitOrUnderscore(t *testing.T) {
	for _, tc := range []struct{ name, kind, secret string }{
		{"AWS access key", "aws-key", "AKIA" + fill("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", 16)},
		{"GitHub token", "github-token", "ghp_" + fill(alnum, 36)},
		{"GitHub fine-grained token", "github-token", "github_pat_" + fill(alnum, 40)},
		{"Slack bot token", "slack-token", "xox" + "b-" + "123456789012-" + fill(alnum, 24)},
		{"Stripe live secret key", "stripe-key", "sk_" + "live_" + fill(alnum, 24)},
		{"JWT", "jwt", "eyJ" + fill(alnum, 12) + ".eyJ" + fill(alnum, 12) + "." + fill(alnum, 12)},
		{"OpenAI key", "api-key", "sk-proj-" + fill(alnum, 48)},
		{"Google API key", "google-api-key", "AIza" + fill(alnum, 35)},
		{"GitLab token", "gitlab-token", "glpat-" + fill(alnum, 20)},
		{"npm token", "npm-token", "npm_" + fill(alnum, 36)},
		{"SendGrid key", "sendgrid-key", "SG." + fill(alnum, 22) + "." + fill(alnum, 43)},
		{"DigitalOcean token", "digitalocean-token", "dop_v1_" + fill(hexs, 64)},
		{"Twilio API key", "twilio-key", "SK" + fill(hexs, 32)},
		{"Shopify token", "shopify-token", "shpat_" + fill(hexs, 32)},
		{"PyPI token", "pypi-token", "pypi-AgEIcHlwaS5vcmc" + fill(alnum, 60)},
		{"Azure AD client secret", "azure-client-secret", "abc8Q~" + fill(alnum, 34)},
		{"Atlassian token", "atlassian-token", "ATATT3" + fill(alnum, 186)},
		{"Hugging Face token", "huggingface-token", "hf_" + fill(alnum, 34)},
	} {
		glue := []string{"build7", "AUTH_", "x_9", "_", "0"}
		if tc.kind == "huggingface-token" {
			// "hf_" after an underscore is left to the entropy check; see
			// tokenAfterNonWord.
			glue = []string{"build7", "0"}
		}
		for _, before := range glue {
			t.Run(tc.name+"/"+before, func(t *testing.T) {
				input := "use " + before + tc.secret + " now"
				got := (Redactor{}).Text(input)
				want := "use " + before + marker(tc.kind, tc.secret) + " now"
				if got != want {
					t.Fatalf("Text(%q) = %q; want %q", input, got, want)
				}
				if twice := (Redactor{}).Text(got); twice != got {
					t.Fatalf("redaction changed on a second pass: %q -> %q", got, twice)
				}
			})
		}
	}
}

// JSON embedded in a string (a tool argument, a log line, a serialized
// request) escapes its quotes; the value after a secret-like name must still
// be masked, and the escapes kept so the outer string stays valid.
func TestEscapedJSONCredentialValues(t *testing.T) {
	env := func(value string) string { return marker("env", value) }
	for _, tc := range []struct{ name, input, want string }{
		{"escaped password", `{\"username\": \"admin\", \"password\": \"hunter2\"}`, `{\"username\": \"admin\", \"password\": \"` + env("hunter2") + `\"}`},
		{"escaped api key without spaces", `"{\"api_key\":\"open sesame\"}"`, `"{\"api_key\":\"` + env("open sesame") + `\"}"`},
		{"double escaped token", `{\\\"access_token\\\": \\\"abc123\\\"}`, `{\\\"access_token\\\": \\\"` + env("abc123") + `\\\"}`},
		{"escaped value after a plain assignment", `DB_PASSWORD=\"Summer2024!\"`, `DB_PASSWORD=\"` + env("Summer2024!") + `\"`},
		{"tool call arguments", `{"command":"curl -d '{\"client_secret\": \"s3cr3t value\"}'"}`, `{"command":"curl -d '{\"client_secret\": \"` + env("s3cr3t value") + `\"}'"}`},
		{"escaped null is unchanged", `{\"password\": null}`, `{\"password\": null}`},
		{"escaped empty value is unchanged", `{\"password\": \"\"}`, `{\"password\": \"\"}`},
		{"escaped number is unchanged", `{\"max_tokens\": 4096}`, `{\"max_tokens\": 4096}`},
		{"escaped list is unchanged", `{\"keywords\": [\"redact\"]}`, `{\"keywords\": [\"redact\"]}`},
		{"escaped non-secret name is unchanged", `{\"token_type\": \"bearer\"}`, `{\"token_type\": \"bearer\"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (Redactor{}).Text(tc.input)
			if got != tc.want {
				t.Fatalf("Text(%q) = %q; want %q", tc.input, got, tc.want)
			}
			if twice := (Redactor{}).Text(got); twice != got {
				t.Fatalf("redaction changed on a second pass: %q -> %q", got, twice)
			}
		})
	}
}

// Relaxing \b must not reach into words, snake_case identifiers or encoded
// blobs that merely contain a credential prefix.
func TestRelaxedBoundaryFalsePositives(t *testing.T) {
	for _, input := range []string{
		"task-runner_and_risk_assessment_module_loader",
		"disk-cache-directory-path-for-the-build-system",
		"flask-sqlalchemy-migrations-and-extensions-v2",
		"load_hf_AutoModelForSequenceClassificationLargeV2(path)",
		"BASIA" + fill("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", 16),
		"payload 0AKIA" + fill("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", 16) + "xyz",
		"run_task_test_" + fill(alnum, 20),
		"QmFzZTY0ZW5jb2RlZGJsb2I0eyJhbGciOiJIUzI1NiJ9dGhpcyBpcyBub3QgYSBqd3Q",
		"v1_2_3ghp_ is the prefix of a GitHub token",
		"x86_64-linux-gnu-gcc-12-sk-",
	} {
		if got := (Redactor{}).Text(input); got != input {
			t.Errorf("false positive: %q -> %q", input, got)
		}
	}
}
