//go:build linux

package daemon

import (
	"os"
	"strconv"
	"strings"
)

// processStartTime reads a Linux process' start time from /proc. Together with
// the PID it identifies a process instance: a recycled PID gets a new value.
func processStartTime(pid int) (uint64, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	stat := string(data)
	// The command name is parenthesized and may contain spaces or ')' itself.
	closeParen := strings.LastIndex(stat, ")")
	if closeParen < 0 || closeParen+1 >= len(stat) {
		return 0, false
	}
	fields := strings.Fields(stat[closeParen+1:])
	// After the command, field 3 is state; starttime is field 22, i.e. index 19.
	if len(fields) < 20 {
		return 0, false
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	return start, err == nil
}
