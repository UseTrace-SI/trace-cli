//go:build !unix

package redact

// lockMarkerKey is a no-op where advisory file locks are unavailable. Trace
// supports macOS and Linux; other platforms do not build today, since
// internal/gitx and internal/daemon use flock as well.
func lockMarkerKey(string) (func(), error) { return func() {}, nil }
