package why

import (
	"strconv"
	"strings"
	"unicode"
)

// Formatters change whitespace, indentation, quote style, statement
// semicolons, trailing commas and where long expressions wrap. None of these
// changes who wrote a line, so lines left unmatched by exact alignment are
// compared again as normalized token sequences.

const (
	maxNormalizedEdits  = 512
	maxReflowTokens     = 20000
	maxNormalizedLCSLen = 2000
)

// matchNormalized fills unmatched entries of matches whose lines differ from
// an older line only in formatting. It works inside each gap between exactly
// matched lines, so a reformatted line can only match its own counterpart.
func matchNormalized(a, b []string, matches []Match) {
	var oldTokens, newTokens [][]string
	previous := 0
	for j := 0; j < len(b); {
		if matches[j].Line > 0 {
			previous = matches[j].Line
			j++
			continue
		}
		start := j
		for j < len(b) && matches[j].Line == 0 {
			j++
		}
		next := len(a) + 1
		if j < len(b) {
			next = matches[j].Line
		}
		if next-1 <= previous {
			continue
		}
		if oldTokens == nil {
			oldTokens, newTokens = lineTokens(a), lineTokens(b)
		}
		matchGap(oldTokens, newTokens, previous, next-1, start, j, matches)
	}
}

// matchGap aligns old lines [oldStart, oldEnd) with new lines [newStart,
// newEnd), first line by line and then token by token for rewrapped lines.
func matchGap(oldTokens, newTokens [][]string, oldStart, oldEnd, newStart, newEnd int, matches []Match) {
	oldKeys := make([]string, oldEnd-oldStart)
	for i := range oldKeys {
		oldKeys[i] = lineKey(oldTokens[oldStart+i], "o", i)
	}
	newKeys := make([]string, newEnd-newStart)
	for j := range newKeys {
		newKeys[j] = lineKey(newTokens[newStart+j], "n", j)
	}
	pairs, ok := alignKeys(oldKeys, newKeys)
	if !ok {
		return
	}
	for j, i := range pairs {
		if i >= 0 {
			matches[newStart+j] = Match{Line: oldStart + i + 1, Normalized: true}
		}
	}
	// Lines still unmatched between line-level matches may have been
	// rewrapped: joined, split or both.
	previous := oldStart
	for j := newStart; j < newEnd; {
		if matches[j].Line > 0 {
			previous = matches[j].Line
			j++
			continue
		}
		start := j
		for j < newEnd && matches[j].Line == 0 {
			j++
		}
		next := oldEnd + 1
		if j < newEnd {
			next = matches[j].Line
		}
		if next-1 > previous {
			matchReflow(oldTokens, newTokens, previous, next-1, start, j, matches)
		}
	}
}

// lineKey joins a line's tokens. Lines without tokens (blank lines) get a
// unique key so they never match by normalization.
func lineKey(tokens []string, side string, i int) string {
	if len(tokens) == 0 {
		return "\x01" + side + strconv.Itoa(i)
	}
	return strings.Join(tokens, "\x00")
}

func alignKeys(a, b []string) ([]int, bool) {
	if len(a) <= maxNormalizedLCSLen && len(b) <= maxNormalizedLCSLen {
		return lcsPairs(a, b), true
	}
	return myersPairs(a, b, maxNormalizedEdits)
}

// matchReflow maps new lines whose tokens all come from old lines whose
// tokens all reappear among them. A line that gained or lost any token
// changed meaning and stays unmatched.
func matchReflow(oldTokens, newTokens [][]string, oldStart, oldEnd, newStart, newEnd int, matches []Match) {
	type token struct {
		text string
		line int
	}
	flatten := func(lines [][]string, start, end int) []token {
		var out []token
		for line := start; line < end; line++ {
			for _, text := range lines[line] {
				out = append(out, token{text, line})
			}
		}
		return out
	}
	oldFlat, newFlat := flatten(oldTokens, oldStart, oldEnd), flatten(newTokens, newStart, newEnd)
	if len(oldFlat) == 0 || len(newFlat) == 0 || len(oldFlat)+len(newFlat) > maxReflowTokens {
		return
	}
	oldText := make([]string, len(oldFlat))
	for i, t := range oldFlat {
		oldText[i] = t.text
	}
	newText := make([]string, len(newFlat))
	for i, t := range newFlat {
		newText[i] = t.text
	}
	var pairs []int
	if len(oldText) <= maxNormalizedLCSLen && len(newText) <= maxNormalizedLCSLen {
		pairs = lcsPairs(oldText, newText)
	} else {
		var ok bool
		if pairs, ok = myersPairs(oldText, newText, maxNormalizedEdits); !ok {
			return
		}
	}
	// Old lines are nodes 0..oldLines-1, new lines follow them.
	oldLines := oldEnd - oldStart
	parent := make([]int, oldLines+newEnd-newStart)
	for i := range parent {
		parent[i] = i
	}
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	oldMatched := make([]bool, len(oldFlat))
	for j, i := range pairs {
		if i < 0 {
			continue
		}
		oldMatched[i] = true
		x, y := find(oldFlat[i].line-oldStart), find(oldLines+newFlat[j].line-newStart)
		if x != y {
			parent[x] = y
		}
	}
	impure := map[int]bool{}
	for j, i := range pairs {
		if i < 0 {
			impure[find(oldLines+newFlat[j].line-newStart)] = true
		}
	}
	for i, matched := range oldMatched {
		if !matched {
			impure[find(oldFlat[i].line-oldStart)] = true
		}
	}
	votes := map[int]map[int]int{}
	for j, i := range pairs {
		if i < 0 {
			continue
		}
		line := newFlat[j].line
		if votes[line] == nil {
			votes[line] = map[int]int{}
		}
		votes[line][oldFlat[i].line]++
	}
	for line, counts := range votes {
		if impure[find(oldLines+line-newStart)] {
			continue
		}
		best, bestCount := -1, 0
		for old, count := range counts {
			if count > bestCount || (count == bestCount && old < best) {
				best, bestCount = old, count
			}
		}
		matches[line] = Match{Line: best + 1, Normalized: true}
	}
}

// lineTokens returns the normalized tokens of each line. The whole text is
// scanned at once so a trailing comma can be recognized before a closing
// bracket on a later line.
func lineTokens(lines []string) [][]string {
	type token struct {
		text string
		line int
	}
	var all []token
	for i, line := range lines {
		for _, text := range scanTokens(line) {
			all = append(all, token{text, i})
		}
	}
	out := make([][]string, len(lines))
	for k, t := range all {
		if t.text == ";" {
			continue
		}
		if t.text == "," {
			next := ""
			for _, u := range all[k+1:] {
				if u.text != ";" {
					next = u.text
					break
				}
			}
			if next == ")" || next == "]" || next == "}" {
				continue
			}
		}
		out[t.line] = append(out[t.line], t.text)
	}
	return out
}

// scanTokens splits a line into words, string literals and punctuation,
// ignoring whitespace. Single- and double-quoted strings are equivalent.
func scanTokens(line string) []string {
	var out []string
	rs := []rune(line)
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case unicode.IsSpace(r):
			i++
		case r == '\'' || r == '"' || r == '`':
			var b strings.Builder
			j := i + 1
			for j < len(rs) && rs[j] != r {
				if rs[j] == '\\' && j+1 < len(rs) {
					if rs[j+1] != '\'' && rs[j+1] != '"' {
						b.WriteRune(rs[j])
					}
					b.WriteRune(rs[j+1])
					j += 2
					continue
				}
				b.WriteRune(rs[j])
				j++
			}
			quote := `"`
			if r == '`' {
				quote = "`"
			}
			out = append(out, quote+b.String()+quote)
			i = j + 1
		case isWordRune(r):
			j := i
			for j < len(rs) && isWordRune(rs[j]) {
				j++
			}
			out = append(out, string(rs[i:j]))
			i = j
		default:
			out = append(out, string(r))
			i++
		}
	}
	return out
}

func isWordRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
