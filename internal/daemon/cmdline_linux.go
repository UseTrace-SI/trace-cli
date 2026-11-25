//go:build linux

package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// processCommandLine reads /proc/<pid>/cmdline, so identifying the daemon
// does not depend on ps being installed.
func processCommandLine(pid int) (string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return "", fmt.Errorf("reading process command line: %w", err)
	}
	// Kernel threads and zombies have an empty command line; neither is a
	// daemon, so the empty string is returned without an error.
	return strings.TrimSpace(strings.ReplaceAll(strings.TrimRight(string(data), "\x00"), "\x00", " ")), nil
}

// processZombie reports whether pid has exited and awaits its parent's
// wait: such a process no longer serves the repository.
func processZombie(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	end := strings.LastIndexByte(string(data), ')')
	return end >= 0 && end+2 < len(data) && data[end+2] == 'Z'
}
