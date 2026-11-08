package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mcpserver "github.com/UseTrace-SI/trace-cli/internal/mcp"
)

func TestContextOutputFormats(t *testing.T) {
	root := competitiveRepo(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	// Exercise formatting without depending on companion installation or startup.
	t.Setenv("PATH", bin)
	for _, tt := range []struct {
		name   string
		format string
		json   bool
		want   string
	}{
		{"json format", "json", false, "json"},
		{"global json", "text", true, "json"},
		{"global json overrides prompt", "prompt", true, "json"},
		{"text", "text", false, "history:"},
		{"prompt", "prompt", false, "## Trace context"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			jsonOutput := tt.json
			cmd := contextCommand(&root, &jsonOutput)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"authentication", "--format", tt.format})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if tt.want == "json" {
				var got mcpserver.ContextOutput
				if err := json.Unmarshal(out.Bytes(), &got); err != nil {
					t.Fatalf("invalid JSON context: %s: %v", out.String(), err)
				}
				if got.Brain.Status != "not_installed" || got.Graph.Status != "not_installed" {
					t.Fatalf("missing component status: %+v", got)
				}
			} else if !strings.HasPrefix(out.String(), tt.want) {
				t.Fatalf("expected %q output, got %s", tt.want, out.String())
			}
		})
	}
}

func TestContextRejectsUnknownFormat(t *testing.T) {
	root := t.TempDir()
	jsonOutput := false
	cmd := contextCommand(&root, &jsonOutput)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"authentication", "--format", "yaml"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unsupported context format") {
		t.Fatalf("expected invalid format error, got %v", err)
	}
}
