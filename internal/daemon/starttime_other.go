//go:build !linux && !darwin

package daemon

// processStartTime is unavailable without /proc or ps support. On those
// platforms the daemon falls back to the legacy command-line identity check.
func processStartTime(int) (uint64, bool) { return 0, false }
