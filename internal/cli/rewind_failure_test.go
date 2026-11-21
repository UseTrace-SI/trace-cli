package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/spf13/cobra"
)

// A rewind that fails after its safety keyframe is saved must leave the tree
// alone and still name the step that undoes it, in text and --json output.
func TestRewindFailureNamesUndoStep(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command func(*string, *bool) *cobra.Command
		args    []string
		json    bool
	}{
		{"rewind", rewindCommand, []string{"0"}, false},
		{"rewind json", rewindCommand, []string{"0"}, true},
		{"resume", resumeCommand, []string{"0", "--no-launch"}, false},
		{"resume json", resumeCommand, []string{"0", "--no-launch"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := competitiveRepo(t)
			for name, body := range map[string]string{"a.txt": "a1\n", "b.txt": "b1\n"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			e, err := engine.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.Baseline(); err != nil {
				t.Fatal(err)
			}
			base, err := e.ManifestAt(0)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			h := string(base["b.txt"].Hash) // lost, as after an interrupted GC
			if err := os.Remove(filepath.Join(root, ".trace", "objects", h[:2], h[2:])); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{"b.txt": "b2\n", "new-work.txt": "unsaved idea\n"}
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			jsonOutput := tc.json
			cmd := tc.command(&root, &jsonOutput)
			cmd.SilenceUsage = true // as under the trace root command
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)
			err = cmd.Execute()
			if err == nil {
				t.Fatal("rewind succeeded without its object")
			}
			e, openErr := engine.Open(root)
			if openErr != nil {
				t.Fatal(openErr)
			}
			var safety int64
			scanErr := e.DB.QueryRow("SELECT seq FROM keyframes WHERE tag='pre-rewind'").Scan(&safety)
			e.Close()
			if scanErr != nil {
				t.Fatalf("no safety keyframe: %v", scanErr)
			}
			undo := fmt.Sprintf("trace rewind %d", safety)
			if !strings.Contains(err.Error(), undo) {
				t.Errorf("error does not name the undo step %q: %v", undo, err)
			}
			if tc.json {
				var result struct {
					SafetyStep int64  `json:"safety_step"`
					Error      string `json:"error"`
				}
				if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil || result.SafetyStep != safety || result.Error == "" {
					t.Errorf("--json output lacks the safety step %d and error: %q, %v", safety, out.String(), decodeErr)
				}
			}
			for name, body := range files {
				if b, readErr := os.ReadFile(filepath.Join(root, name)); readErr != nil || string(b) != body {
					t.Errorf("failed rewind changed %s: %q, %v", name, b, readErr)
				}
			}
		})
	}
}
