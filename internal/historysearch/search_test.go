package historysearch

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMakeSnippetKeepsRuneBoundaries(t *testing.T) {
	// The match and both window edges deliberately land in the middle of
	// three-byte characters when measured in bytes.
	term := "needle"
	withMatch := strings.Repeat("a", 7) + strings.Repeat("界", 100) + term + strings.Repeat("界", 100)
	for _, tc := range []struct {
		name, text, term string
	}{
		{"match with multibyte surroundings", withMatch, term},
		{"truncation without a match", strings.Repeat("界", 100), "missing"},
		{"ascii unchanged", "plain ascii text", "ascii"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := makeSnippet(tc.text, tc.term)
			if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
				t.Fatalf("snippet contains a split rune: %q", got)
			}
		})
	}
}

func TestTerms(t *testing.T) {
	for _, tc := range []struct {
		query, want string
		invalid     bool
	}{
		{"sign-in", `("sign-in")`, false},
		{"deadlock|panic sqlite", `("deadlock" OR "panic") AND ("sqlite")`, false},
		{"  café  API  ", `("café") AND ("API")`, false},
		{"", "", false},
		{"|", "", true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			got, err := terms(tc.query, false)
			if (err != nil) != tc.invalid || got != tc.want {
				t.Fatalf("terms(%q) = %q, %v; want %q, invalid=%t", tc.query, got, err, tc.want, tc.invalid)
			}
		})
	}
}
