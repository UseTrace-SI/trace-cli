package why

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

func TestMapLine(t *testing.T) {
	for _, tc := range []struct {
		old, new   string
		line, want int
		touched    bool
	}{
		{"a\nb\nc\n", "a\nx\nb\nc\n", 3, 2, false},
		{"a\nb\nc\n", "a\nx\nb\nc\n", 2, 0, true},
		{"a\nb\nc\n", "a\nB\nc\n", 2, 0, true},
		{"a\nb\nc\n", "a\nc\n", 2, 3, false},
	} {
		got, touched := MapLine(tc.old, tc.new, tc.line)
		if got != tc.want || touched != tc.touched {
			t.Errorf("map %q -> %q line %d: got %d,%t want %d,%t", tc.old, tc.new, tc.line, got, touched, tc.want, tc.touched)
		}
	}
}

func TestMapLineRandomEditSequences(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for sequence := 0; sequence < 50; sequence++ {
		old := make([]string, 20)
		for i := range old {
			old[i] = fmt.Sprintf("original-%d", i)
		}
		for edit := 0; edit < 30; edit++ {
			newLines := slices.Clone(old)
			at := rng.Intn(len(newLines) + 1)
			newText := fmt.Sprintf("new-%d-%d", sequence, edit)
			switch rng.Intn(3) {
			case 0:
				newLines = slices.Insert(newLines, at, newText)
			case 1:
				if len(newLines) > 0 {
					at %= len(newLines)
					newLines = slices.Delete(newLines, at, at+1)
				}
			case 2:
				if len(newLines) > 0 {
					at %= len(newLines)
					newLines[at] = newText
				}
			}
			oldText := strings.Join(old, "\n") + "\n"
			newTextBody := strings.Join(newLines, "\n") + "\n"
			for line, content := range newLines {
				got, touched := MapLine(oldText, newTextBody, line+1)
				previous := slices.Index(old, content)
				if previous < 0 && (!touched || got != 0) {
					t.Fatalf("sequence %d edit %d line %d: inserted line mapped to %d", sequence, edit, line+1, got)
				}
				if previous >= 0 && (touched || got != previous+1) {
					t.Fatalf("sequence %d edit %d line %d: unchanged line mapped to %d,%t; want %d", sequence, edit, line+1, got, touched, previous+1)
				}
			}
			old = newLines
		}
	}
}
