package redact

import (
	"math"
	"regexp"
	"strings"
)

type Redactor struct{ PII bool }

var patterns = []struct {
	kind string
	rx   *regexp.Regexp
}{
	{"private-key", regexp.MustCompile(`(?s)(-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----.*?-----END (?:[A-Z0-9 ]+ )?PRIVATE KEY-----|-----BEGIN PGP PRIVATE KEY BLOCK-----.*?-----END PGP PRIVATE KEY BLOCK-----)`)},
	{"aws-key", token(`(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"github-token", token(`(?:ghp_|gho_|ghu_|ghs_|ghr_|github_pat_)[A-Za-z0-9_]{20,}\b`)},
	{"slack-token", token(`xox[abeoprs]-[A-Za-z0-9-]{10,}\b|xapp-[0-9]-[A-Za-z0-9-]{10,}\b`)},
	{"slack-webhook", regexp.MustCompile(`https://hooks\.slack\.com/(?:services|workflows|triggers)/[A-Za-z0-9/_-]{20,}`)},
	{"stripe-key", token(`(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b|whsec_[A-Za-z0-9+/=]{24,}`)},
	{"jwt", token(`eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	// OpenAI (sk-..., sk-proj-..., sk-svcacct-...) and Anthropic (sk-ant-...)
	// keys share this prefix.
	{"api-key", token(`sk-[A-Za-z0-9_-]{20,}\b`)},
	{"google-api-key", token(`AIza[0-9A-Za-z_-]{35}`)},
	{"gitlab-token", token(`(?:glpat|glptt|gldt|glrt|glcbt|glft|glsoat)-[0-9A-Za-z_-]{20,}`)},
	{"npm-token", token(`npm_[A-Za-z0-9]{36}\b`)},
	{"huggingface-token", tokenAfterNonWord(`hf_[A-Za-z0-9]{34,}\b`)},
	{"sendgrid-key", token(`SG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}`)},
	{"digitalocean-token", token(`do[opr]_v1_[a-f0-9]{64}\b`)},
	{"twilio-key", token(`SK[0-9a-fA-F]{32}\b`)},
	{"shopify-token", token(`shp(?:at|ca|pa|ss)_[a-fA-F0-9]{32}\b`)},
	{"pypi-token", token(`pypi-AgEIcHlwaS5vcmc[A-Za-z0-9_-]{50,}`)},
	{"azure-client-secret", token(`[A-Za-z0-9_~.]{3}[0-9]Q~[A-Za-z0-9_~.-]{31,34}`)},
	{"atlassian-token", token(`ATATT3[A-Za-z0-9_=-]{180,}`)},
	{"url-password", regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s:/@]*:[^\s@/]+@`)},
	// An HTTP Authorization header value: the scheme and an RFC 6750 token
	// (Basic credentials are base64, a subset). The credential needs a digit
	// or 16 characters, so a word after the scheme ("an Authorization: Bearer
	// token header", "Bearer authentication") is left alone.
	{"auth-header", regexp.MustCompile(`\b(?:[Aa]uthorization|AUTHORIZATION)["']?\s*(?:=>|[:=,])\s*["']?(?i:bearer|basic|token)[ \t]+(?:[A-Za-z._~+/-]*[0-9][A-Za-z0-9._~+/-]*|[A-Za-z0-9._~+/-]{16,})=*`)},
}

// token compiles a credential pattern that must not continue a word: it may
// start the text or follow any character except an ASCII letter. Unlike \b it
// also matches after a digit or underscore ("1ghp_…", "KEY_AKIA…"), which
// commonly glue a credential to a label or counter, while a preceding letter
// ("task-…", "disk-…") still rules it out. The credential is submatch s; the
// leading character is kept.
func token(body string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^A-Za-z])(?P<s>` + body + `)`)
}

// tokenAfterNonWord keeps the \b behaviour after an underscore. "hf_" with
// 34 letters is also a plausible snake_case identifier tail
// ("load_hf_AutoModel…"), so only a digit or non-word character may precede.
func tokenAfterNonWord(body string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^A-Za-z_])(?P<s>` + body + `)`)
}

// Necessary literal fragments cheaply rule out patterns before invoking the
// regexp engine on large transcripts. A fragment alone never causes redaction.
var patternHints = map[string][]string{
	"private-key":         {"PRIVATE KEY-----", "PGP PRIVATE KEY BLOCK-----"},
	"aws-key":             {"AKIA", "ASIA"},
	"github-token":        {"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"},
	"slack-token":         {"xox", "xapp-"},
	"slack-webhook":       {"hooks.slack.com/"},
	"stripe-key":          {"sk_", "rk_", "whsec_"},
	"jwt":                 {"eyJ"},
	"api-key":             {"sk-"},
	"google-api-key":      {"AIza"},
	"gitlab-token":        {"glpat-", "glptt-", "gldt-", "glrt-", "glcbt-", "glft-", "glsoat-"},
	"npm-token":           {"npm_"},
	"huggingface-token":   {"hf_"},
	"sendgrid-key":        {"SG."},
	"digitalocean-token":  {"_v1_"},
	"twilio-key":          {"SK"},
	"shopify-token":       {"shpat_", "shpca_", "shppa_", "shpss_"},
	"pypi-token":          {"pypi-AgEIcHlwaS5vcmc"},
	"azure-client-secret": {"Q~"},
	"atlassian-token":     {"ATATT3"},
	"url-password":        {"://"},
	"auth-header":         {"uthorization", "UTHORIZATION"},
}

// envValue matches a secret-like name, an assignment or comparison operator
// (group 2) and its value (group 3). A quoted, JSON-style name must end with
// the secret word; group 1 holds its closing quote, which may be escaped when
// the JSON is itself inside a string (`{\"password\": \"…\"}`), and such a
// value runs to the next escaped quote. Multi-character operators come first
// so their trailing character is never taken as the value.
var envValue = regexp.MustCompile(`(?im)\b[A-Z0-9_]*(?:KEY|SECRET|TOKEN|PASSWORD|PASSWD)(?:[A-Z0-9_]*|(\\*["']))\s*(===?|!==?|:=|=>|[:=])\s*(\\+"[^"\n]*?\\+"|"[^"\n]*"|'[^'\n]*'|["']?[^\s,"']+)`)
var redactionMarker = regexp.MustCompile(`⟨redacted:[a-z-]+:[0-9a-f]{8}⟩`)
var email = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
var phone = regexp.MustCompile(`\b\+?[0-9][0-9 ()-]{8,}[0-9]\b`)
var candidate = regexp.MustCompile(`[A-Za-z0-9_+/=-]{12,}`)
var keyContext = regexp.MustCompile(`(?i)(key|secret|token|password|passwd|api)`)

func (r Redactor) Text(s string) string {
	if s == "" {
		return s
	}
	for _, p := range patterns {
		possible := false
		for _, hint := range patternHints[p.kind] {
			if strings.Contains(s, hint) {
				possible = true
				break
			}
		}
		if !possible {
			continue
		}
		s = replaceMatches(p.rx, s, p.kind)
	}
	context := hasKeyContext(s)
	if context {
		s = redactEnvValues(s)
	}
	original := s
	var indexes [][]int
	if context {
		indexes = candidate.FindAllStringIndex(original, -1)
	}
	for i := len(indexes) - 1; i >= 0; i-- {
		start, end := indexes[i][0], indexes[i][1]
		from := start - 30
		if from < 0 {
			from = 0
		}
		value := original[start:end]
		if strings.Contains(original[from:start], "⟨redacted:") || !keyContext.MatchString(original[from:start]) || entropy(value) <= 3.5 {
			continue
		}
		s = s[:start] + marker("entropy", value) + s[end:]
	}
	if r.PII {
		s = email.ReplaceAllStringFunc(s, func(v string) string { return marker("email", v) })
		s = phone.ReplaceAllStringFunc(s, func(v string) string { return marker("phone", v) })
	}
	return s
}

// replaceMatches replaces each match of rx with a marker, or only its
// submatch s when the pattern has one.
func replaceMatches(rx *regexp.Regexp, s, kind string) string {
	group := rx.SubexpIndex("s")
	if group < 0 {
		return rx.ReplaceAllStringFunc(s, func(v string) string { return marker(kind, v) })
	}
	matches := rx.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	var out strings.Builder
	last := 0
	for _, m := range matches {
		start, end := m[2*group], m[2*group+1]
		out.WriteString(s[last:start])
		out.WriteString(marker(kind, s[start:end]))
		last = end
	}
	out.WriteString(s[last:])
	return out.String()
}

func hasKeyContext(s string) bool {
	lower := strings.ToLower(s)
	for _, word := range []string{"key", "secret", "token", "password", "passwd", "api"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	// Unicode case-insensitive regular expressions also fold long s to S.
	return strings.ContainsRune(s, '\u017f') && keyContext.MatchString(s)
}

func redactEnvValues(s string) string {
	// Every environment assignment requires a separator. Avoid the expensive
	// case-insensitive name matcher on large source/transcript strings without
	// one; this preserves exactly the same redaction behavior.
	if !strings.ContainsAny(s, "=:") {
		return s
	}
	matches := envValue.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	markers := redactionMarker.FindAllStringIndex(s, -1)
	var out strings.Builder
	last, nextMarker := 0, 0
	for _, match := range matches {
		for nextMarker < len(markers) && markers[nextMarker][1] <= match[0] {
			nextMarker++
		}
		if nextMarker < len(markers) && markers[nextMarker][0] <= match[0] && match[0] < markers[nextMarker][1] {
			continue
		}
		start, end := match[6], match[7]
		value := s[start:end]
		if value[0] == '\\' {
			// An escaped string literal: mask what lies between the
			// escaped quotes and keep the escapes.
			open := strings.IndexByte(value, '"') + 1
			close := strings.LastIndexByte(value, '\\')
			for close > open && value[close-1] == '\\' {
				close--
			}
			if close <= open {
				continue
			}
			start, end, value = start+open, start+close, value[open:close]
			if location := redactionMarker.FindStringIndex(value); location != nil && location[0] == 0 && location[1] == len(value) {
				continue
			}
			out.WriteString(s[last:start])
			out.WriteString(marker("env", value))
			last = end
			continue
		}
		quoted := value[0] == '"' || value[0] == '\''
		// Only a plain assignment takes a bare value. After a quoted name, a
		// comparison or a hash rocket a credential is a string literal, so
		// identifiers, numbers and null (`token != nil`, `key => key.id`,
		// `"password": null`) are left alone.
		if op := s[match[4]:match[5]]; !quoted && (match[2] >= 0 || (op != "=" && op != ":" && op != ":=")) {
			continue
		}
		// Keep the quotes of a complete string so the surrounding code stays
		// balanced and a second pass recognizes the marker.
		if quoted && len(value) > 1 && value[len(value)-1] == value[0] {
			start, end, value = start+1, end-1, value[1:len(value)-1]
		}
		unquoted := strings.Trim(value, `"'`)
		if unquoted == "" {
			continue
		}
		if location := redactionMarker.FindStringIndex(unquoted); location != nil && location[0] == 0 && location[1] == len(unquoted) {
			continue
		}
		out.WriteString(s[last:start])
		out.WriteString(marker("env", value))
		last = end
	}
	out.WriteString(s[last:])
	return out.String()
}

func entropy(s string) float64 {
	if s == "" {
		return 0
	}
	counts := map[byte]int{}
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	var result float64
	for _, n := range counts {
		p := float64(n) / float64(len(s))
		result -= p * math.Log2(p)
	}
	return result
}
