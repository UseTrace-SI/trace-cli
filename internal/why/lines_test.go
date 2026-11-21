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

func TestMapLineLargeFileWithLocalEdit(t *testing.T) {
	old := make([]string, 3000)
	for i := range old {
		old[i] = fmt.Sprintf("unique-line-%04d", i)
	}
	newLines := slices.Insert(slices.Clone(old), 100, "inserted-line")
	before := strings.Join(old, "\n") + "\n"
	after := strings.Join(newLines, "\n") + "\n"
	for _, tc := range []struct {
		line, want int
		touched    bool
	}{{50, 50, false}, {101, 0, true}, {102, 101, false}, {2501, 2500, false}} {
		got, touched := MapLine(before, after, tc.line)
		if got != tc.want || touched != tc.touched {
			t.Errorf("line %d: got %d,%t want %d,%t", tc.line, got, touched, tc.want, tc.touched)
		}
	}
	withTwoEdits := slices.Insert(slices.Clone(newLines), 2900, "another-inserted-line")
	got, touched := MapLine(before, strings.Join(withTwoEdits, "\n")+"\n", 1501)
	if got != 1500 || touched {
		t.Errorf("line between distant edits: got %d,%t want 1500,false", got, touched)
	}
	withRepeats := slices.Clone(old)
	for i := 0; i < len(withRepeats); i += 10 {
		withRepeats[i] = "}"
	}
	repeatedEdits := slices.Insert(slices.Clone(withRepeats), 100, "first-insertion")
	repeatedEdits = slices.Insert(repeatedEdits, 2900, "second-insertion")
	got, touched = MapLine(strings.Join(withRepeats, "\n")+"\n", strings.Join(repeatedEdits, "\n")+"\n", 1502)
	if got != 1501 || touched {
		t.Errorf("repeated line between distant edits: got %d,%t want 1501,false", got, touched)
	}
}

func TestMapLineLargeRandomEdits(t *testing.T) {
	rng := rand.New(rand.NewSource(73))
	old := make([]string, 3000)
	for i := range old {
		old[i] = fmt.Sprintf("original-%04d", i)
	}
	before := strings.Join(old, "\n") + "\n"
	for sequence := 0; sequence < 15; sequence++ {
		updated := slices.Clone(old)
		origin := make([]int, len(old))
		for i := range origin {
			origin[i] = i + 1
		}
		for edit := 0; edit < 20; edit++ {
			at := rng.Intn(len(updated))
			switch rng.Intn(3) {
			case 0:
				updated = slices.Insert(updated, at, fmt.Sprintf("new-%d-%d", sequence, edit))
				origin = slices.Insert(origin, at, 0)
			case 1:
				updated = slices.Delete(updated, at, at+1)
				origin = slices.Delete(origin, at, at+1)
			case 2:
				updated[at] = fmt.Sprintf("new-%d-%d", sequence, edit)
				origin[at] = 0
			}
		}
		after := strings.Join(updated, "\n") + "\n"
		check := map[int]bool{}
		for line := 1; line <= len(updated); line += 223 {
			check[line] = true
		}
		for i, original := range origin {
			if original == 0 {
				for line := max(1, i); line <= min(len(updated), i+2); line++ {
					check[line] = true
				}
			}
		}
		for line := range check {
			got, touched := MapLine(before, after, line)
			if got != origin[line-1] || touched != (origin[line-1] == 0) {
				t.Fatalf("sequence %d line %d: got %d,%t want %d,%t", sequence, line, got, touched,
					origin[line-1], origin[line-1] == 0)
			}
		}
	}
}

func TestMapLineReportsAmbiguousLargeDiff(t *testing.T) {
	old := strings.Repeat("}\n", 3000)
	updated := make([]string, 3000)
	for i := range updated {
		if i%2 == 0 {
			updated[i] = fmt.Sprintf("new-%d", i)
		} else {
			updated[i] = "}"
		}
	}
	newText := strings.Join(updated, "\n") + "\n"
	if oldLine, touched := MapLine(old, newText, 1500); oldLine != -1 || touched {
		t.Fatalf("ambiguous repeated line = %d,%t; want -1,false", oldLine, touched)
	}
	if oldLine, touched := MapLine(old, newText, 1499); oldLine != 0 || !touched {
		t.Fatalf("new line = %d,%t; want 0,true", oldLine, touched)
	}
}

func TestMapLinesMatchesFormattingOnlyChanges(t *testing.T) {
	for _, tc := range []struct {
		name, old, new string
		want           []Match
	}{
		{"gofmt indentation", "func f() {\n    x := 1\n    return x\n}\n", "func f() {\n\tx := 1\n\treturn x\n}\n",
			[]Match{{1, false}, {2, true}, {3, true}, {4, false}}},
		{"operator spacing", "a\nx:=b+c\nz\n", "a\nx := b + c\nz\n", []Match{{1, false}, {2, true}, {3, false}}},
		{"quotes and semicolons", "const a = 'x'\nconst b = 2\n", "const a = \"x\";\nconst b = 2;\n", []Match{{1, true}, {2, true}}},
		{"escaped quotes", "say('don\\'t')\n", "say(\"don't\")\n", []Match{{1, true}}},
		{"wrapped call with trailing comma", "x\nfoo(alpha, beta)\ny\n", "x\nfoo(\n  alpha,\n  beta,\n)\ny\n",
			[]Match{{1, false}, {2, true}, {2, true}, {2, true}, {2, true}, {3, false}}},
		{"unwrapped call", "foo(\n  alpha,\n  beta,\n)\n", "foo(alpha, beta)\n", []Match{{1, true}}},
		{"semantic change while reindenting", "f() {\n    return x\n}\n", "f() {\n\treturn y\n}\n", []Match{{1, false}, {0, false}, {3, false}}},
		{"argument removed while wrapping", "foo(alpha, beta, gamma)\n", "foo(\n  alpha,\n  beta,\n)\n", []Match{{0, false}, {0, false}, {0, false}, {0, false}}},
		{"string content changed", "s = 'abc'\n", "s = \"abd\"\n", []Match{{0, false}}},
		{"blank line inserted", "a\nb\n", "a\n  \nb\n", []Match{{1, false}, {0, false}, {2, false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := MapLines(tc.old, tc.new)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("MapLines = %v; want %v", got, tc.want)
			}
		})
	}
}

// The exact tier of MapLines must agree with MapLine; normalization may only
// add matches for lines MapLine reports as changed.
func TestMapLinesExactTierAgreesWithMapLine(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for sequence := 0; sequence < 40; sequence++ {
		old := make([]string, 30)
		for i := range old {
			old[i] = fmt.Sprintf("value_%d = call(arg%d)", i%7, i%5)
			if rng.Intn(4) == 0 {
				old[i] = "}"
			}
		}
		updated := slices.Clone(old)
		for edit := 0; edit < 8; edit++ {
			at := rng.Intn(len(updated))
			switch rng.Intn(4) {
			case 0:
				updated = slices.Insert(updated, at, fmt.Sprintf("inserted_%d()", edit))
			case 1:
				updated = slices.Delete(updated, at, at+1)
			case 2:
				updated[at] = "  " + strings.ReplaceAll(updated[at], " ", "")
			case 3:
				updated[at] = fmt.Sprintf("changed_%d", edit)
			}
		}
		oldText, newText := strings.Join(old, "\n")+"\n", strings.Join(updated, "\n")+"\n"
		got := MapLines(oldText, newText)
		for line := range updated {
			want, touched := MapLine(oldText, newText, line+1)
			match := got[line]
			if !match.Normalized && (match.Line != want || (match.Line == 0) != touched) {
				t.Fatalf("sequence %d line %d: MapLines %v, MapLine %d,%t", sequence, line+1, match, want, touched)
			}
			if match.Normalized && !touched {
				t.Fatalf("sequence %d line %d: normalized match for an exact line %d", sequence, line+1, want)
			}
		}
	}
}

func TestMapLinesReportsAmbiguousLargeDiff(t *testing.T) {
	old := strings.Repeat("}\n", 3000)
	updated := make([]string, 3000)
	for i := range updated {
		updated[i] = "}"
		if i%2 == 0 {
			updated[i] = fmt.Sprintf("new-%d", i)
		}
	}
	got := MapLines(old, strings.Join(updated, "\n")+"\n")
	if got[1499].Line != -1 || got[1498].Line != 0 {
		t.Fatalf("large ambiguous diff = %v, %v", got[1499], got[1498])
	}
}
