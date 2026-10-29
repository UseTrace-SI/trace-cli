package hookcmd

import (
	"strings"
	"testing"
)

func TestCommandUsesAbsolutePath(t *testing.T) {
	command := Command("claude-code", "Stop")
	if strings.HasPrefix(command, "trace hook ") {
		t.Fatalf("command is not absolute: %q", command)
	}
	if !strings.HasSuffix(command, " hook claude-code Stop") {
		t.Fatalf("command = %q", command)
	}
}

func TestShellPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"plain", "/usr/local/bin/trace", "/usr/local/bin/trace"},
		{"space", "/Users/a b/trace", `'/Users/a b/trace'`},
		{"single quote", "/tmp/it's/trace", `'/tmp/it'\''s/trace'`},
		{"dollar", "/tmp/$x/trace", `'/tmp/$x/trace'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellPath(tc.in); got != tc.want {
				t.Fatalf("shellPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsTraceHookFor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		agent   string
		event   string
		want    bool
	}{
		{"legacy", "trace hook cursor stop", "cursor", "stop", true},
		{"absolute", "/opt/trace hook cursor stop", "cursor", "stop", true},
		{"quoted absolute", "'/opt/my trace' hook cursor stop", "cursor", "stop", true},
		{"trailing space", "trace hook cursor stop  ", "cursor", "stop", true},
		{"wrong event", "trace hook cursor sessionStart", "cursor", "stop", false},
		{"wrong agent", "trace hook gemini stop", "cursor", "stop", false},
		{"unrelated", "echo stop", "cursor", "stop", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTraceHookFor(tc.command, tc.agent, tc.event); got != tc.want {
				t.Fatalf("IsTraceHookFor(%q) = %t, want %t", tc.command, got, tc.want)
			}
		})
	}
}

func TestIsTraceHook(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		agent   string
		want    bool
	}{
		{"legacy", "trace hook agy Stop", "agy", true},
		{"absolute", "/usr/local/bin/trace hook agy Stop", "agy", true},
		{"quoted absolute", "'/Users/a b/trace' hook agy Stop", "agy", true},
		{"quoted apostrophe", `'/tmp/it'\''s/trace' hook agy Stop`, "agy", true},
		{"other executable", "/usr/bin/other hook agy Stop", "agy", false},
		{"quoted other executable", "'/usr/bin/other' hook agy Stop", "agy", false},
		{"shell compound", "/bin/echo hi; trace hook agy Stop", "agy", false},
		{"unrelated prefix", "mytrace hook agy Stop", "agy", false},
		{"other agent", "trace hook gemini Stop", "agy", false},
		{"not a hook", "trace daemon", "agy", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTraceHook(tc.command, tc.agent); got != tc.want {
				t.Fatalf("IsTraceHook(%q, %q) = %t, want %t", tc.command, tc.agent, got, tc.want)
			}
		})
	}
}
