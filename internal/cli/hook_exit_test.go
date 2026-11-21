package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildTrace compiles the real binary, so tests observe the exit status and
// output an agent sees from a hook process.
func buildTrace(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "trace")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/UseTrace-SI/trace-cli/cmd/trace")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %s: %v", out, err)
	}
	return bin
}

// Global hooks read each repository's own agent configuration to avoid
// duplicating a project hook. That file is repository content, so no value in
// it may make the hook exit non-zero (status 2 blocks Gemini CLI and Claude
// Code) or suppress the response an agent waits for on stdout.
func TestGlobalHookExitsZeroOnHostileProjectConfig(t *testing.T) {
	bin := buildTrace(t)
	for _, tc := range []struct {
		agent, event, file, config, stdout string
	}{
		{"gemini-global", "AfterTool", ".gemini/settings.json", `{"hooks":{"AfterTool":[{"hooks":[{"command":"' hook gemini AfterTool"}]}]}}`, "{}\n"},
		{"cursor-global", "beforeReadFile", ".cursor/hooks.json", `{"hooks":{"beforeReadFile":[{"command":"' hook cursor beforeReadFile"}]}}`, `{"permission":"allow"}` + "\n"},
		{"qwen-global", "PostToolUse", ".qwen/settings.json", `{"hooks":{"PostToolUse":[{"hooks":[{"command":"' hook qwen PostToolUse"}]}]}}`, "{}\n"},
		{"copilot-global", "postToolUse", ".github/hooks/usetrace.json", `{"version":1,"hooks":{"postToolUse":[{"type":"command","command":"' hook copilot postToolUse"}]}}`, ""},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			root := t.TempDir()
			if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
				t.Fatalf("git init: %s: %v", out, err)
			}
			if err := os.MkdirAll(filepath.Join(root, filepath.Dir(tc.file)), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, tc.file), []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "hook", tc.agent, tc.event)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "TRACE_HOME="+t.TempDir())
			cmd.Stdin = bytes.NewBufferString(fmt.Sprintf(`{"session_id":"s","cwd":%q,"workspace_roots":[%q],"toolName":"edit"}`, root, root))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				t.Fatalf("hook exited with status %d (stdout %q, stderr %q)", exit.ExitCode(), stdout.String(), firstLine(stderr.String()))
			}
			if err != nil {
				t.Fatal(err)
			}
			if stdout.String() != tc.stdout {
				t.Fatalf("hook stdout = %q, want %q", stdout.String(), tc.stdout)
			}
		})
	}
}

func firstLine(s string) string {
	if i := bytes.IndexByte([]byte(s), '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
