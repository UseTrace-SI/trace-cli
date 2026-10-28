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
	{"private-key", regexp.MustCompile(`(?s)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----.*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`)},
	{"aws-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"github-token", regexp.MustCompile(`\b(?:ghp_|gho_|ghu_|ghs_|ghr_|github_pat_)[A-Za-z0-9_]{20,}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{"stripe-key", regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	{"api-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
	{"url-password", regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s:/@]+:[^\s@/]+@`)},
}

var envValue = regexp.MustCompile(`(?im)\b[A-Z0-9_]*(?:KEY|SECRET|TOKEN|PASSWORD|PASSWD)[A-Z0-9_]*\s*[:=]\s*(["']?[^\s,"']+["']?)`)
var email = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
var phone = regexp.MustCompile(`\b\+?[0-9][0-9 ()-]{8,}[0-9]\b`)
var candidate = regexp.MustCompile(`[A-Za-z0-9_+/=-]{20,}`)
var keyContext = regexp.MustCompile(`(?i)(key|secret|token|password|passwd|api)`)

func marker(kind, value string) string {
	h := sha256.Sum256([]byte(value))
	return "⟨redacted:" + kind + ":" + hex.EncodeToString(h[:])[:8] + "⟩"
}

func (r Redactor) Text(s string) string {
	for _, p := range patterns {
		s = p.rx.ReplaceAllStringFunc(s, func(v string) string { return marker(p.kind, v) })
	}
	s = envValue.ReplaceAllStringFunc(s, func(v string) string {
		i := strings.IndexAny(v, "=:")
		if i < 0 {
			return v
		}
		if strings.Contains(v, "⟨redacted:") {
			return v
		}
		return v[:i+1] + marker("env", strings.TrimSpace(v[i+1:]))
	})
	original := s
	indexes := candidate.FindAllStringIndex(original, -1)
	for i := len(indexes) - 1; i >= 0; i-- {
		start, end := indexes[i][0], indexes[i][1]
		from := start - 30
		if from < 0 {
			from = 0
		}
		value := original[start:end]
		if strings.Contains(original[from:start], "⟨redacted:") || !keyContext.MatchString(original[from:start]) || entropy(value) <= 4.0 {
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
