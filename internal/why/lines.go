package why

import "strings"

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Match maps one line of a newer text to the older text it came from.
type Match struct {
	// Line is the 1-based line in the older text. Zero means the newer line
	// was inserted or changed; -1 means a large, ambiguous diff could not be
	// aligned reliably.
	Line int
	// Normalized is set when the line matched only after whitespace and
	// formatting differences were ignored.
	Normalized bool
}

// MapLine maps a line in the newer text to its matching line in the older text.
// A zero old line means the newer line was inserted or replaced. A negative
// old line means a large, ambiguous diff could not be mapped reliably.
func MapLine(oldText, newText string, line int) (int, bool) {
	b := split(newText)
	if line < 1 || line > len(b) {
		return 0, true
	}
	match := mapExact(split(oldText), b)[line-1]
	if match.Line < 0 {
		return -1, false
	}
	return match.Line, match.Line == 0
}

// MapLines maps every line of newText to oldText. Lines are first aligned
// exactly; lines that only changed formatting (whitespace, quote style,
// semicolons, trailing commas or line wrapping) then match with Normalized
// set. Lines that changed otherwise map to zero.
func MapLines(oldText, newText string) []Match {
	a, b := split(oldText), split(newText)
	matches := mapExact(a, b)
	for _, match := range matches {
		if match.Line < 0 {
			return matches
		}
	}
	matchNormalized(a, b, matches)
	return matches
}

// mapExact aligns identical lines. It keeps the established behaviour of
// MapLine: a common prefix and suffix, a full LCS for moderate middles, a
// bounded Myers diff for large ones and unique-line matching after that.
func mapExact(a, b []string) []Match {
	n, m := len(a), len(b)
	out := make([]Match, m)
	prefix := 0
	for prefix < n && prefix < m && a[prefix] == b[prefix] {
		out[prefix].Line = prefix + 1
		prefix++
	}
	suffix := 0
	for suffix < n-prefix && suffix < m-prefix && a[n-suffix-1] == b[m-suffix-1] {
		out[m-suffix-1].Line = n - suffix
		suffix++
	}
	a, b = a[prefix:n-suffix], b[prefix:m-suffix]
	n, m = len(a), len(b)
	if m == 0 {
		return out
	}
	if n > 2000 || m > 2000 {
		if pairs, ok := myersPairs(a, b, 512); ok {
			for j, i := range pairs {
				if i >= 0 {
					out[prefix+j].Line = prefix + i + 1
				}
			}
			return out
		}
		oldCount := map[string]int{}
		oldAt := map[string]int{}
		for i, value := range a {
			oldCount[value]++
			oldAt[value] = i
		}
		newCount := map[string]int{}
		for _, value := range b {
			newCount[value]++
		}
		for j, value := range b {
			switch {
			case oldCount[value] == 1 && newCount[value] == 1:
				out[prefix+j].Line = prefix + oldAt[value] + 1
			case oldCount[value] == 0:
				out[prefix+j].Line = 0
			default:
				out[prefix+j].Line = -1
			}
		}
		return out
	}
	for j, i := range lcsPairs(a, b) {
		if i >= 0 {
			out[prefix+j].Line = prefix + i + 1
		}
	}
	return out
}

// lcsPairs returns, for each element of b, the matched index in a or -1.
func lcsPairs[T comparable](a, b []T) []int {
	n, m := len(a), len(b)
	width := m + 1
	dp := make([]uint16, (n+1)*width)
	at := func(i, j int) int { return int(dp[i*width+j]) }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				dp[i*width+j] = dp[(i+1)*width+j+1] + 1
			case at(i+1, j) >= at(i, j+1):
				dp[i*width+j] = dp[(i+1)*width+j]
			default:
				dp[i*width+j] = dp[i*width+j+1]
			}
		}
	}
	pairs := make([]int, m)
	for j := range pairs {
		pairs[j] = -1
	}
	for i, j := 0, 0; i < n || j < m; {
		if i < n && j < m && a[i] == b[j] {
			pairs[j] = i
			i++
			j++
			continue
		}
		if i < n && (j == m || at(i+1, j) >= at(i, j+1)) {
			i++
		} else {
			j++
		}
	}
	return pairs
}

// myersPairs aligns a and b with at most maxEdits insertions and deletions.
// Most code changes have few edits even when the surrounding file is long.
func myersPairs[T comparable](a, b []T, maxEdits int) ([]int, bool) {
	n, m := len(a), len(b)
	limit := min(n+m, maxEdits)
	offset := limit + 1
	v := make([]int, 2*limit+3)
	trace := make([][]int, 0, limit+1)
	for d := 0; d <= limit; d++ {
		next := make([]int, len(v))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			next[offset+k] = x
			if x == n && y == m {
				trace = append(trace, next)
				return backtrackPairs(trace, offset, n, m), true
			}
		}
		trace = append(trace, next)
		v = next
	}
	return nil, false
}

func backtrackPairs(trace [][]int, offset, n, m int) []int {
	pairs := make([]int, m)
	for j := range pairs {
		pairs[j] = -1
	}
	x, y := n, m
	for d := len(trace) - 1; d > 0; d-- {
		previous := trace[d-1]
		k := x - y
		var prevK int
		if k == -d || (k != d && previous[offset+k-1] < previous[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := previous[offset+prevK]
		prevY := prevX - prevK
		snakeX, snakeY := prevX, prevY
		if prevK == k+1 {
			snakeY++
		} else {
			snakeX++
		}
		for x > snakeX && y > snakeY {
			pairs[y-1] = x - 1
			x--
			y--
		}
		x, y = prevX, prevY
	}
	for x > 0 && y > 0 {
		pairs[y-1] = x - 1
		x--
		y--
	}
	return pairs
}
