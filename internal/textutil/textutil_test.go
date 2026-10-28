package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"empty", "", 5, ""},
		{"shorter than limit", "abc", 5, "abc"},
		{"exact limit", "abcde", 5, "abcde"},
		{"cuts ascii", "abcdef", 3, "abc"},
		{"zero limit", "abc", 0, ""},
		{"negative limit", "abc", -1, ""},
		{"multibyte", "héllo", 2, "hé"},
		{"emoji", "🙂🙂🙂", 2, "🙂🙂"},
		{"combining marks are separate runes", "e\u0301x", 1, "e"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Truncate(tc.in, tc.max); got != tc.want {
				t.Fatalf("Truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestTruncateBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"empty", "", 5, ""},
		{"fits", "abc", 5, "abc"},
		{"ascii cut", "abcdef", 3, "abc"},
		{"zero limit", "abc", 0, ""},
		{"whole multibyte", "héllo", 3, "hé"},
		{"partial multibyte trimmed", "héllo", 2, "h"},
		{"emoji trimmed", "🙂", 3, ""},
		{"emoji fits", "🙂", 4, "🙂"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateBytes(tc.in, tc.max)
			if got != tc.want {
				t.Fatalf("TruncateBytes(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("TruncateBytes(%q, %d) = invalid UTF-8 %q", tc.in, tc.max, got)
			}
			if len(got) > tc.max {
				t.Fatalf("TruncateBytes(%q, %d) returned %d bytes", tc.in, tc.max, len(got))
			}
		})
	}
}

func TestTruncateBytesLargeMultibyteInput(t *testing.T) {
	in := strings.Repeat("🙂", 2000)
	got := TruncateBytes(in, 4001)
	if len(got) != 4000 || !utf8.ValidString(got) {
		t.Fatalf("got %d bytes, valid=%t", len(got), utf8.ValidString(got))
	}
	if !strings.HasPrefix(in, got) {
		t.Fatal("truncation did not preserve a prefix of the input")
	}
}
