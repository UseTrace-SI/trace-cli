// Package hookcmd builds and recognizes Trace agent hook commands. Absolute
// executable paths keep hooks working when an agent starts with a minimal PATH.
package hookcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Path returns the current executable, resolved through symlinks where
// possible.
func Path() string {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		return exe
	}
	if exe, err := exec.LookPath("trace"); err == nil {
		return exe
	}
	return "trace"
}

// Command returns a shell command for one agent hook.
func Command(agent, event string) string {
	return shellPath(Path()) + " hook " + agent + " " + event
}

func shellPath(path string) string {
	if !strings.ContainsAny(path, " \t\n'\"\\$`&;|<>()*?[]{}!#~") {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// IsTraceHookFor reports whether command invokes the given agent event. It
// recognizes both legacy bare `trace hook ...` commands and absolute paths.
func IsTraceHookFor(command, agent, event string) bool {
	return strings.HasSuffix(strings.TrimSpace(command), " hook "+agent+" "+event)
}

// IsTraceHook reports whether command is any Trace hook for agent.
func IsTraceHook(command, agent string) bool {
	trimmed := strings.TrimSpace(command)
	idx := strings.LastIndex(trimmed, " hook "+agent+" ")
	if idx < 0 {
		return false
	}
	prefix := trimmed[:idx]
	return prefix == "trace" || strings.HasPrefix(prefix, "'") || filepath.IsAbs(prefix)
}
