// Package textutil provides small, allocation-light string helpers shared by
// the CLI and MCP surfaces.
package textutil

import "unicode/utf8"

// Truncate returns the first maxRunes runes of s. It never splits a UTF-8
// sequence.
func Truncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

// TruncateBytes returns at most maxBytes of s, trimming a partial trailing
// rune when the cut would otherwise produce invalid UTF-8.
func TruncateBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	b := []byte(s[:maxBytes])
	for len(b) > 0 {
		r, size := utf8.DecodeLastRune(b)
		if r != utf8.RuneError || size > 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return string(b)
}
