package why

import "strings"

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// MapLine maps a line in the newer text to its matching line in the older text.
// A zero old line means the newer line was inserted or replaced. A negative
// old line means a large, ambiguous diff could not be mapped reliably.
func MapLine(oldText, newText string, line int) (int, bool) {
	a, b := split(oldText), split(newText)
	if line < 1 || line > len(b) {
		return 0, true
	}
	n, m := len(a), len(b)
	prefix := 0
	for prefix < n && prefix < m && a[prefix] == b[prefix] {
		prefix++
	}
	if line <= prefix {
		return line, false
	}
	suffix := 0
	for suffix < n-prefix && suffix < m-prefix && a[n-suffix-1] == b[m-suffix-1] {
		suffix++
	}
	if line > m-suffix {
		return line + n - m, false
	}
	a, b = a[prefix:n-suffix], b[prefix:m-suffix]
	line -= prefix
	n, m = len(a), len(b)
	if n > 2000 || m > 2000 {
		if oldLine, touched, exact := myersMapLine(a, b, line); exact {
			if touched {
				return 0, true
			}
			return prefix + oldLine, false
		}
		target := b[line-1]
		oldAt, oldCount, newCount := -1, 0, 0
		for i, value := range a {
			if value == target {
				oldAt, oldCount = i, oldCount+1
			}
		}
		for _, value := range b {
			if value == target {
				newCount++
			}
		}
		if oldCount == 1 && newCount == 1 {
			return prefix + oldAt + 1, false
		}
		if oldCount == 0 {
			return 0, true
		}
		return -1, false
	}
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	for i, j := 0, 0; i < n || j < m; {
		if i < n && j < m && a[i] == b[j] {
			if j+1 == line {
				return prefix + i + 1, false
			}
			i++
			j++
			continue
		}
		if i < n && (j == m || dp[i+1][j] >= dp[i][j+1]) {
			i++
		} else {
			if j+1 == line {
				return 0, true
			}
			j++
		}
	}
	return 0, true
}

// myersMapLine resolves large diffs with a bounded edit distance. Most code
// changes have few edits even when the surrounding file is long.
func myersMapLine(a, b []string, line int) (oldLine int, touched, exact bool) {
	const maxEdits = 512
	n, m := len(a), len(b)
	v := map[int]int{1: 0}
	trace := make([]map[int]int, 0, min(n+m, maxEdits)+1)
	for d := 0; d <= min(n+m, maxEdits); d++ {
		next := make(map[int]int, 2*d+1)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1] < v[k+1]) {
				x = v[k+1]
			} else {
				x = v[k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			next[k] = x
			if x == n && y == m {
				trace = append(trace, next)
				return backtrackLine(trace, n, m, line)
			}
		}
		trace = append(trace, next)
		v = next
	}
	return 0, false, false
}

func backtrackLine(trace []map[int]int, n, m, line int) (oldLine int, touched, exact bool) {
	x, y := n, m
	for d := len(trace) - 1; d > 0; d-- {
		previous := trace[d-1]
		k := x - y
		var prevK int
		if k == -d || (k != d && previous[k-1] < previous[k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := previous[prevK]
		prevY := prevX - prevK
		snakeX, snakeY := prevX, prevY
		if prevK == k+1 {
			snakeY++
		} else {
			snakeX++
		}
		for x > snakeX && y > snakeY {
			if y == line {
				return x, false, true
			}
			x--
			y--
		}
		if prevK == k+1 && prevY+1 == line {
			return 0, true, true
		}
		x, y = prevX, prevY
	}
	if line <= y {
		return line, false, true
	}
	return 0, true, true
}
