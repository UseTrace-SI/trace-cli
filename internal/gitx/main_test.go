package gitx

import (
	"os"
	"testing"
)

// Installed hooks invoke the current executable as `<bin> hook git ...`. In
// tests that is this binary, which must then exit like a successful hook.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		os.Exit(0)
	}
	os.Exit(m.Run())
}
