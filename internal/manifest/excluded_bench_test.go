package manifest

import (
	"fmt"
	"testing"
)

// benchPaths is a repository-shaped path set: mostly nested source files,
// with a few dot-files and configuration files mixed in.
func benchPaths(n int) []string {
	shapes := []string{
		"internal/pkg%d/handler_%d.go",
		"web/src/components/Widget%d/index_%d.tsx",
		"docs/guide/section%d/page_%d.md",
		"services/api%d/config/settings_%d.yaml",
		"vendor/github.com/org%d/lib/file_%d.go",
		"packages/app%d/.eslintrc_%d.json",
	}
	paths := make([]string, n)
	for i := range paths {
		paths[i] = fmt.Sprintf(shapes[i%len(shapes)], i%97, i)
	}
	return paths
}

func BenchmarkExcluded100k(b *testing.B) {
	paths := benchPaths(100000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range paths {
			_ = excluded(p)
		}
	}
}
