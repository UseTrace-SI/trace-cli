package redact

import (
	"math"
	"regexp"
	"sort"
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
// the JSON is itself inside a string (`{\"password\": \"…\"}`). Group 4 then
// holds the escaped opening quote of the value, whose end escapedValueEnd
// finds. Multi-character operators come first so their trailing character is
// never taken as the value.
var envValue = regexp.MustCompile(`(?im)\b[A-Z0-9_]*(?:KEY|SECRET|TOKEN|PASSWORD|PASSWD)(?:[A-Z0-9_]*|(\\*["']))\s*(===?|!==?|:=|=>|[:=])\s*((\\+")|"[^"\n]*"|'[^'\n]*'|["']?[^\s,"']+)`)
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
		// Masking can join or shift quotes and escapes so that a second
		// reading finds more (an escaped value whose closing escape was
		// masked runs to the line end). Repeat until nothing changes, so
		// redacting the output again leaves it as it is.
		originals := map[string]envOriginal{}
		for i := 0; i < maxEnvPasses; i++ {
			next := redactEnvValues(s, originals, i > 0)
			if next == s {
				break
			}
			s = next
			if settled(s, originals) {
				break
			}
		}
		s = redactEntropy(s, originals)
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

// settled reports that another environment pass cannot change s: every
// marker of this call stands alone, ending at a separator and, when it is
// followed by a quote, opened by the same unescaped quote. A value a pass
// could read again would have to run into or out of such a marker.
func settled(s string, originals map[string]envOriginal) bool {
	for _, loc := range redactionMarker.FindAllStringIndex(s, -1) {
		if _, ok := originals[s[loc[0]:loc[1]]]; !ok {
			continue
		}
		if loc[1] == len(s) {
			continue
		}
		switch c := s[loc[1]]; c {
		case ' ', '\t', '\n', '\r', ',':
			continue
		case '"', '\'':
			if loc[0] > 0 && s[loc[0]-1] == c && (loc[0] < 2 || s[loc[0]-2] != '\\') {
				continue
			}
		}
		return false
	}
	return true
}

// maxEnvPasses bounds the passes that bring environment-value redaction to a
// fixed point; real text settles in one or two.
const maxEnvPasses = 4

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

// envOriginal is the value an environment marker replaced. plainEnd is
// where, within it, the last stretch the plain reading alone (the rules
// before escaped quotes were read) would also have masked ends, or -1:
// before that point those rules saw a marker, after it plain text.
type envOriginal struct {
	text     string
	plainEnd int
}

func redactEnvValues(s string, originals map[string]envOriginal, later bool) string {
	// Every environment assignment requires a separator. Avoid the expensive
	// case-insensitive name matcher on large source/transcript strings without
	// one; this preserves exactly the same redaction behavior.
	if !strings.ContainsAny(s, "=:") {
		return s
	}
	markers := redactionMarker.FindAllStringIndex(s, -1)
	// The escaped-quote rules can read a stretch of text differently from
	// the plain rules (an escaped quote can open a value that would
	// otherwise hold the next name). Masking the union of both readings
	// never masks less than the plain rules alone.
	plain := plainEnvSpans(s, markers)
	spans := mergeSpans(append(envSpans(s, markers), plain...))
	sort.Slice(plain, func(i, j int) bool { return plain[i][0] < plain[j][0] })
	if len(spans) == 0 {
		return s
	}
	var out strings.Builder
	last, p := 0, 0
	for _, span := range spans {
		out.WriteString(s[last:span[0]])
		m := marker("env", s[span[0]:span[1]])
		if originals != nil {
			// plain is sorted and inside the merged spans, so it is walked
			// once alongside them.
			plainEnd := -1
			for ; !later && p < len(plain) && plain[p][0] < span[1]; p++ {
				if span[0] < plain[p][1] {
					plainEnd = max(plainEnd, min(plain[p][1], span[1])-span[0])
				}
			}
			originals[m] = envOriginal{text: s[span[0]:span[1]], plainEnd: plainEnd}
		}
		out.WriteString(m)
		last = span[1]
	}
	out.WriteString(s[last:])
	return out.String()
}

// plainEnvValue is envValue without escaped quotes.
var plainEnvValue = regexp.MustCompile(`(?im)\b[A-Z0-9_]*(?:KEY|SECRET|TOKEN|PASSWORD|PASSWD)(?:[A-Z0-9_]*|(["']))\s*(===?|!==?|:=|=>|[:=])\s*("[^"\n]*"|'[^'\n]*'|["']?[^\s,"']+)`)

// plainEnvSpans reads assignments with plainEnvValue, matches never
// overlapping.
func plainEnvSpans(s string, markers [][]int) [][2]int {
	var spans [][2]int
	for _, match := range plainEnvValue.FindAllStringSubmatchIndex(s, -1) {
		if insideMarker(markers, match[0]) {
			continue
		}
		if start, end, ok := valueSpan(s, match); ok {
			spans = append(spans, [2]int{start, end})
		}
	}
	return spans
}

// envSpans reads assignments with envValue, including escaped string values.
// A value that is skipped is scanned again from its start, so an assignment
// inside or right after it is still found.
func envSpans(s string, markers [][]int) [][2]int {
	var spans [][2]int
	for pos := 0; pos < len(s); {
		match := findEnvValue(s, pos)
		if match == nil {
			break
		}
		pos = match[6]
		if insideMarker(markers, match[0]) {
			continue
		}
		if match[8] >= 0 {
			// An escaped string literal: mask up to the matching escaped
			// quote, keeping the escapes; unterminated, to the end of the line.
			start := match[9]
			end := escapedValueEnd(s, start, match[9]-match[8]-1)
			pos = max(end, start)
			if value := s[start:end]; value != "" && !isMarker(value) {
				spans = append(spans, [2]int{start, end})
			}
			continue
		}
		if start, end, ok := valueSpan(s, match); ok {
			spans = append(spans, [2]int{start, end})
			pos = end
		}
	}
	return spans
}

// valueSpan returns the part of a matched plain value to mask, or false when
// the value is not a credential.
func valueSpan(s string, match []int) (int, int, bool) {
	start, end := match[6], match[7]
	value := s[start:end]
	quoted := value[0] == '"' || value[0] == '\''
	// Only a plain assignment takes a bare value. After a quoted name, a
	// comparison or a hash rocket a credential is a string literal, so
	// identifiers, numbers and null (`token != nil`, `key => key.id`,
	// `"password": null`) are left alone.
	if op := s[match[4]:match[5]]; !quoted && (match[2] >= 0 || (op != "=" && op != ":" && op != ":=")) {
		return 0, 0, false
	}
	// Keep the quotes of a complete string so the surrounding code stays
	// balanced and a second pass recognizes the marker.
	if quoted && len(value) > 1 && value[len(value)-1] == value[0] {
		start, end, value = start+1, end-1, value[1:len(value)-1]
	}
	// Backslashes that escape the quote after a bare value belong to that
	// quote: masking them would leave a bare quote that a second pass reads
	// as opening a new value.
	if !quoted && end < len(s) && (s[end] == '"' || s[end] == '\'') {
		for end > start && s[end-1] == '\\' {
			end--
		}
		value = s[start:end]
	}
	unquoted := strings.Trim(value, `"'`)
	if unquoted == "" || isMarker(unquoted) {
		return 0, 0, false
	}
	return start, end, true
}

// insideMarker reports whether position at falls inside a marker.
func insideMarker(markers [][]int, at int) bool {
	i := sort.Search(len(markers), func(i int) bool { return markers[i][1] > at })
	return i < len(markers) && markers[i][0] <= at
}

// mergeSpans sorts spans and joins overlapping ones.
func mergeSpans(spans [][2]int) [][2]int {
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	var out [][2]int
	for _, span := range spans {
		if n := len(out); n > 0 && span[0] < out[n-1][1] {
			out[n-1][1] = max(out[n-1][1], span[1])
			continue
		}
		out = append(out, span)
	}
	return out
}

// findEnvValue finds the next envValue match at or after pos, with indexes
// into s. Searching a suffix loses what precedes it, which only matters to
// the leading \b: a name cannot start inside a word, so the search first
// skips the rest of any word pos is in.
func findEnvValue(s string, pos int) []int {
	if pos > 0 && isWordByte(s[pos-1]) {
		for pos < len(s) && isWordByte(s[pos]) {
			pos++
		}
	}
	match := envValue.FindStringSubmatchIndex(s[pos:])
	for i := range match {
		if match[i] >= 0 {
			match[i] += pos
		}
	}
	return match
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isMarker(s string) bool {
	location := redactionMarker.FindStringIndex(s)
	return location != nil && location[0] == 0 && location[1] == len(s)
}

// escapedValueEnd returns where a string value opened by a quote escaped with
// n backslashes ends: before the n backslashes escaping its closing quote, or
// at the line end. Each nesting level doubles backslashes and escapes quotes,
// so at this level a literal backslash in the value adds 2(n+1) backslashes
// and a literal quote is preceded by n+(n+1): a quote ends the value only
// when its run of backslashes is n plus a multiple of 2(n+1).
func escapedValueEnd(s string, from, n int) int {
	for i := from; i < len(s); i++ {
		switch s[i] {
		case '\n':
			return i
		case '"':
			j := i
			for j > from && s[j-1] == '\\' {
				j--
			}
			if run := i - j; run >= n && (run-n)%(2*(n+1)) == 0 {
				return i - n
			}
		}
	}
	return len(s)
}

// contextKind names marker kinds that stand for provider credentials: text
// right after one (an AWS secret after its access key, a CSV column after a
// token) is in key context. Unlike the platform's share redactor, "env" is
// not one: an assignment's value is masked by name, and treating it as
// context masked ordinary neighbours (`key=…, plaintext=128`) in the Go
// standard-library corpus.
var contextKind = regexp.MustCompile(`key|token|secret|password|passwd|api|auth|credential`)

// contextWindow returns the 30 bytes before start in which a secret word
// gives key context. An environment marker of this call reads as the value
// it replaced (and markers inside that value as theirs), so masking a value
// never removes the context it held, except where the plain rules would
// also have masked it: the window ends there, as it does at any other
// marker (the words before a masked value named it).
func contextWindow(s string, before [][]int, start int, originals map[string]envOriginal) string {
	window, _ := windowBefore(s, before, start, 30, originals, 0)
	return window
}

// windowBefore collects up to need bytes before end of s, whose markers
// (ending at or before end) are given, expanding markers as contextWindow
// describes. stopped reports that a marker ended the window early.
func windowBefore(s string, markers [][]int, end, need int, originals map[string]envOriginal, depth int) (window string, stopped bool) {
	pos, i := end, len(markers)-1
	for len(window) < need && pos > 0 {
		floor := 0
		if i >= 0 {
			floor = markers[i][1]
		}
		from := max(floor, pos-(need-len(window)))
		window = s[from:pos] + window
		pos = from
		if pos > floor || i < 0 {
			return window, false
		}
		original, ok := originals[s[markers[i][0]:markers[i][1]]]
		if !ok || depth > maxEnvPasses {
			return window, true
		}
		text := original.text[max(original.plainEnd, 0):]
		inner, innerStopped := windowBefore(text, redactionMarker.FindAllStringIndex(text, -1), len(text), need-len(window), originals, depth+1)
		window = inner + window
		if innerStopped || original.plainEnd >= 0 {
			return window, true
		}
		pos = markers[i][0]
		i--
	}
	return window, false
}

// expandOriginal replaces the environment markers of this call inside text
// with the values they replaced.
func expandOriginal(text string, originals map[string]envOriginal, depth int) string {
	if depth > maxEnvPasses || !strings.Contains(text, "⟨redacted:env:") {
		return text
	}
	return redactionMarker.ReplaceAllStringFunc(text, func(m string) string {
		if original, ok := originals[m]; ok {
			return expandOriginal(original.text, originals, depth+1)
		}
		return m
	})
}

// leadingRun and trailingRun return the candidate characters at the start
// and end of s.
func leadingRun(s string) string {
	if loc := candidateRun.FindStringIndex(s); loc != nil && loc[0] == 0 {
		return s[:loc[1]]
	}
	return ""
}

func trailingRun(s string) string {
	i := len(s)
	for i > 0 && isCandidateByte(s[i-1]) {
		i--
	}
	return s[i:]
}

var candidateRun = regexp.MustCompile(`[A-Za-z0-9_+/=-]+`)

func isCandidateByte(c byte) bool {
	return isWordByte(c) || c == '+' || c == '/' || c == '=' || c == '-'
}

// redactEntropy masks high-entropy candidates near a secret word or right
// after a credential marker. Candidates overlapping a marker are skipped.
// Entropy markers give no context, and a marker is at least as long as the
// 30-byte window, so a second pass finds nothing new. originals maps the
// environment markers of this call to the values they replaced: a candidate
// touching one is judged together with the adjoining characters of that
// value, as it would have been before the value was masked.
func redactEntropy(s string, originals map[string]envOriginal) string {
	markers := redactionMarker.FindAllStringIndex(s, -1)
	var out strings.Builder
	last, next := 0, 0
	// Runs shorter than a candidate still count when they touch a marker of
	// this call, as part of the longer run they formed before masking.
	runs := candidate
	if len(originals) > 0 {
		runs = candidateRun
	}
	for _, c := range runs.FindAllStringIndex(s, -1) {
		start, end := c[0], c[1]
		for next < len(markers) && markers[next][1] <= start {
			next++
		}
		if next < len(markers) && markers[next][0] < end {
			continue
		}
		value := s[start:end]
		joined, maskStart, maskEnd := value, start, end
		if next < len(markers) && markers[next][0] == end {
			if original, ok := originals[s[markers[next][0]:markers[next][1]]]; ok {
				joined += leadingRun(expandOriginal(original.text, originals, 0))
				maskEnd = markers[next][1]
			}
		}
		if next > 0 && markers[next-1][1] == start && markers[next-1][0] >= last {
			if original, ok := originals[s[markers[next-1][0]:markers[next-1][1]]]; ok {
				joined = trailingRun(expandOriginal(original.text, originals, 0)) + joined
				maskStart = markers[next-1][0]
			}
		}
		alone := len(value) >= 12 && entropy(value) > 3.5
		if !alone && (len(joined) < 12 || entropy(joined) <= 3.5) {
			continue
		}
		from := max(0, start-30)
		nearby := false
		if next > 0 && markers[next-1][1] > from {
			kind := s[markers[next-1][0]:markers[next-1][1]]
			kind = strings.TrimPrefix(kind, "⟨redacted:")
			kind, _, _ = strings.Cut(kind, ":")
			nearby = contextKind.MatchString(kind)
		}
		if !nearby && !keyContext.MatchString(contextWindow(s, markers[:next], start, originals)) {
			continue
		}
		if alone {
			maskStart, maskEnd = start, end
		}
		// A run judged together with a touching masked value is masked
		// together with it, as one marker: a run and a marker side by side
		// would read as a new value on a second pass.
		out.WriteString(s[last:maskStart])
		out.WriteString(marker("entropy", s[maskStart:maskEnd]))
		last = maskEnd
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
