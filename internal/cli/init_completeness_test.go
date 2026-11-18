package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

func TestInitializationComplete(t *testing.T) {
	for _, tc := range []struct {
		name string
		skip func(t *testing.T, root, traceDir string)
		want bool
	}{
		{"all artifacts", nil, true},
		{"missing database", func(t *testing.T, _, traceDir string) {
			if err := os.Remove(filepath.Join(traceDir, "trace.db")); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"missing completion marker", func(t *testing.T, _, traceDir string) {
			if err := os.Remove(filepath.Join(traceDir, "init-complete")); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"marker for another agent", func(t *testing.T, _, traceDir string) {
			if err := os.WriteFile(filepath.Join(traceDir, "init-complete"), []byte("codex\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"missing daemon marker", func(t *testing.T, _, traceDir string) {
			if err := os.Remove(filepath.Join(traceDir, "daemon.pid")); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"missing git exclude", func(t *testing.T, root, _ string) {
			p, err := gitExcludePath(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, nil, 0600); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %s: %v", out, err)
			}
			traceDir := repo.TraceDir(root)
			if err := os.MkdirAll(traceDir, 0700); err != nil {
				t.Fatal(err)
			}
			for name, data := range map[string]string{
				"trace.db":      "db",
				"init-complete": "claude-code\n",
				"daemon.pid":    "123",
			} {
				if err := os.WriteFile(filepath.Join(traceDir, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := addExclude(root); err != nil {
				t.Fatal(err)
			}
			if tc.skip != nil {
				tc.skip(t, root, traceDir)
			}
			if got := initializationComplete(root, "claude-code"); got != tc.want {
				t.Fatalf("initializationComplete = %t, want %t", got, tc.want)
			}
		})
	}
}
