package why

import "strings"

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// MapLine maps a line in the newer text to its matching line in the older text.
// A zero old line means the newer line was inserted or replaced.
func MapLine(oldText, newText string, line int) (int, bool) {
	a, b := split(oldText), split(newText)
	if line < 1 || line > len(b) {
		return 0, true
	}
	n, m := len(a), len(b)
	if n > 2000 || m > 2000 {
		if line <= n && a[line-1] == b[line-1] {
			return line, false
		}
		return 0, true
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
				return i + 1, false
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
