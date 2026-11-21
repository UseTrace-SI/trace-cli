package redact

import (
	"crypto/sha256"
	"encoding/hex"
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
	{"aws-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"github-token", regexp.MustCompile(`\b(?:ghp_|gho_|ghu_|ghs_|ghr_|github_pat_)[A-Za-z0-9_]{20,}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{"stripe-key", regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	{"api-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
	{"url-password", regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s:/@]+:[^\s@/]+@`)},
}

// Necessary literal fragments cheaply rule out patterns before invoking the
// regexp engine on large transcripts. A fragment alone never causes redaction.
var patternHints = map[string][]string{
	"private-key":  {"PRIVATE KEY-----", "PGP PRIVATE KEY BLOCK-----"},
	"aws-key":      {"AKIA", "ASIA"},
	"github-token": {"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"},
	"slack-token":  {"xox"},
	"stripe-key":   {"sk_", "rk_"},
	"jwt":          {"eyJ"},
	"api-key":      {"sk-"},
	"url-password": {"://"},
}

// envValue matches a secret-like name, an assignment or comparison operator
// (group 2) and its value (group 3). A quoted, JSON-style name must end with
// the secret word; group 1 holds its closing quote. Multi-character operators
// come first so their trailing character is never taken as the value.
var envValue = regexp.MustCompile(`(?im)\b[A-Z0-9_]*(?:KEY|SECRET|TOKEN|PASSWORD|PASSWD)(?:[A-Z0-9_]*|(["']))\s*(===?|!==?|:=|=>|[:=])\s*("[^"\n]*"|'[^'\n]*'|["']?[^\s,"']+)`)
var redactionMarker = regexp.MustCompile(`⟨redacted:[a-z-]+:[0-9a-f]{8}⟩`)
var email = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
var phone = regexp.MustCompile(`\b\+?[0-9][0-9 ()-]{8,}[0-9]\b`)
var candidate = regexp.MustCompile(`[A-Za-z0-9_+/=-]{12,}`)
var keyContext = regexp.MustCompile(`(?i)(key|secret|token|password|passwd|api)`)

func marker(kind, value string) string {
	h := sha256.Sum256([]byte(value))
	return "⟨redacted:" + kind + ":" + hex.EncodeToString(h[:])[:8] + "⟩"
}

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
		s = p.rx.ReplaceAllStringFunc(s, func(v string) string { return marker(p.kind, v) })
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
