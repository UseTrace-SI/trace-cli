package restore

import (
	"fmt"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

// BenchmarkPlan100k plans a restore between two 100k-file trees that differ
// in 1% of their files, the shape of a rewind across a few turns.
func BenchmarkPlan100k(b *testing.B) {
	current, target := manifest.Manifest{}, manifest.Manifest{}
	for i := 0; i < 100000; i++ {
		p := fmt.Sprintf("internal/pkg%d/file_%d.go", i%97, i)
		current[p] = manifest.Entry{Hash: blob.Hash(fmt.Sprintf("%064d", i)), Mode: 0644}
		target[p] = current[p]
		if i%100 == 0 {
			target[p] = manifest.Entry{Hash: blob.Hash(fmt.Sprintf("%064d", i+1)), Mode: 0644}
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Plan(current, target)
	}
}
