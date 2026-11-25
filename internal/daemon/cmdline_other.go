//go:build !linux

package daemon

func processCommandLine(pid int) (string, error) { return psCommandLine(pid) }
