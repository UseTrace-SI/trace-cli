//go:build !treesitter

package structure

// Classify is enabled by the optional treesitter build tag.
func Classify(_ string, _, _ string) []Change { return nil }
