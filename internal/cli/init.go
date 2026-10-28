package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/agy"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/claudecode"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/cursor"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/gitx"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

func addExclude(root string) error {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		return fmt.Errorf("finding git exclude: %w", err)
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading git exclude: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == ".trace/" {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating git info directory: %w", err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("opening git exclude: %w", err)
	}
	defer f.Close()
	prefix := []byte{}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte{'\n'}) {
		prefix = []byte{'\n'}
	}
	if _, err := f.Write(append(prefix, []byte(".trace/\n")...)); err != nil {
		return fmt.Errorf("writing git exclude: %w", err)
	}
	return nil
}

func initCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var agent string
	cmd := &cobra.Command{Use: "init", RunE: func(c *cobra.Command, _ []string) error {
		if agent != "claude-code" && agent != "codex" && agent != "cursor" && agent != "agy" {
			return fmt.Errorf("unsupported agent %q", agent)
		}
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		if _, err := repo.FindRoot(root); err != nil {
			return err
		}
		if err := os.MkdirAll(repo.TraceDir(root), 0700); err != nil {
			return fmt.Errorf("creating trace directory: %w", err)
		}
		if err := addExclude(root); err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		if agent == "claude-code" {
			if err := claudecode.Install(root); err != nil {
				return err
			}
		} else if agent == "codex" {
			if err := os.WriteFile(filepath.Join(repo.TraceDir(root), "codex.enabled"), []byte("1\n"), 0600); err != nil {
				return fmt.Errorf("enabling Codex adapter: %w", err)
			}
		} else if agent == "cursor" {
			if err := cursor.Install(root); err != nil {
				return err
			}
		} else {
			if err := agy.Install(root); err != nil {
				return err
			}
		}
		if err := e.Baseline(); err != nil {
			return err
		}
		if err := installMCP(root); err != nil {
			return err
		}
		if err := gitx.InstallHook(root); err != nil {
			return err
		}
		if err := daemon.EnsureRunning(root); err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"initialized": true, "repo": root})
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Trace initialized. %s turns will be recorded here.\n", agent)
		return err
	}}
	cmd.Flags().StringVar(&agent, "agent", "claude-code", "agent to integrate")
	return cmd
}
