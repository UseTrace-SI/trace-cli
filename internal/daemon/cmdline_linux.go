//go:build linux

package daemon

import (
	"errors"
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
	command := strings.TrimSpace(strings.ReplaceAll(strings.TrimRight(string(data), "\x00"), "\x00", " "))
	if command == "" {
		return "", errors.New("empty process command line")
	}
	return command, nil
}
