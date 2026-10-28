//go:build !treesitter

package structure

import "testing"

func TestDefaultBuildHasNoStructuralClassification(t *testing.T) {
	if got := Classify("file.go", "func Old() {}", "func New() {}"); len(got) != 0 {
		t.Fatalf("unexpected classifications: %v", got)
	}
}
