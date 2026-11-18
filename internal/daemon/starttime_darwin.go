//go:build darwin

package daemon

import (
	"hash/fnv"
	"os/exec"
	"strconv"
	"strings"
)

// processStartTime identifies a macOS process instance using its start time
// as reported by ps. The PID alone is not enough: after PID reuse the same
// number can belong to a different process. The string is stable for the life
// of a process, so hashing it gives a compact, comparable identity.
func processStartTime(pid int) (uint64, bool) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return 0, false
	}
	start := strings.TrimSpace(string(out))
	if start == "" || strings.HasPrefix(start, "-") {
		return 0, false
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(start))
	return h.Sum64(), true
}
