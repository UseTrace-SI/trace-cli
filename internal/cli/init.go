package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/agy"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/aider"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/amp"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/claudecode"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/cline"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/codebuff"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/copilot"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/crewai"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/cursor"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/factory"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/gemini"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/goose"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/grok"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/kilo"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/kiro"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/langgraph"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/omp"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/openclaude"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/openhands"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/pi"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/qwen"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/windsurf"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/gitx"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/spf13/cobra"
)

func addExclude(root string) error {
	p, err := gitExcludePath(root)
	if err != nil {
		return err
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

func gitExcludePath(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		return "", fmt.Errorf("finding git exclude: %w", err)
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return p, nil
}

func initCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var agent string
	var ifNeeded bool
	cmd := &cobra.Command{Use: "init", RunE: func(c *cobra.Command, _ []string) error {
		if agent != "claude-code" && agent != "codex" && agent != "cursor" && agent != "agy" && agent != "opencode" && agent != "windsurf" && agent != "gemini" && agent != "copilot" && agent != "qwen" && agent != "factory" && agent != "pi" && agent != "omp" && agent != "grok" && agent != "goose" && agent != "amp" && agent != "openclaude" && agent != "openhands" && agent != "kilo" && agent != "kiro" && agent != "langgraph" && agent != "cline" && agent != "crewai" && agent != "aider" && agent != "codebuff" {
			return fmt.Errorf("unsupported agent %q", agent)
		}
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return err
		}
		if _, err := repo.FindRoot(root); err != nil {
			return err
		}
		if ifNeeded {
			if _, err := os.Stat(filepath.Join(repo.TraceDir(root), "trace.db")); err == nil {
				if *jsonOutput {
					return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"initialized": false, "already": true, "repo": root})
				}
				_, err = fmt.Fprintf(c.OutOrStdout(), "Trace already initialized in %s.\n", root)
				return err
			}
		}
		if agent == "openhands" {
			if err := openhands.CheckProject(root); err != nil {
				return err
			}
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
		} else if agent == "openclaude" {
			if err := openclaude.Install(root); err != nil {
				return err
			}
		} else if agent == "openhands" {
			if err := openhands.InstallProject(root); err != nil {
				return err
			}
		} else if agent == "codex" {
			if err := os.WriteFile(filepath.Join(repo.TraceDir(root), "codex.enabled"), []byte("1\n"), 0600); err != nil {
				return fmt.Errorf("enabling Codex adapter: %w", err)
			}
		} else if agent == "opencode" {
			if err := os.WriteFile(filepath.Join(repo.TraceDir(root), "opencode.enabled"), []byte("1\n"), 0600); err != nil {
				return fmt.Errorf("enabling OpenCode adapter: %w", err)
			}
		} else if agent == "cursor" {
			if err := cursor.Install(root); err != nil {
				return err
			}
		} else if agent == "windsurf" {
			if err := windsurf.Install(root); err != nil {
				return err
			}
		} else if agent == "gemini" {
			if err := gemini.Install(root); err != nil {
				return err
			}
		} else if agent == "copilot" {
			if err := copilot.Install(root); err != nil {
				return err
			}
		} else if agent == "qwen" {
			if err := qwen.Install(root); err != nil {
				return err
			}
		} else if agent == "factory" {
			if err := factory.Install(root); err != nil {
				return err
			}
		} else if agent == "pi" {
			if err := pi.Install(root); err != nil {
				return err
			}
		} else if agent == "omp" {
			if err := omp.Install(root); err != nil {
				return err
			}
		} else if agent == "grok" {
			if err := grok.Install(root); err != nil {
				return err
			}
		} else if agent == "goose" {
			if err := goose.Install(root); err != nil {
				return err
			}
		} else if agent == "cline" {
			if err := cline.Install(root); err != nil {
				return err
			}
		} else if agent == "codebuff" {
			if err := codebuff.Install(root); err != nil {
				return err
			}
		} else if agent == "aider" {
			if err := aider.Install(root); err != nil {
				return err
			}
		} else if agent == "crewai" {
			if err := crewai.Install(root); err != nil {
				return err
			}
		} else if agent == "langgraph" {
			if err := langgraph.Install(root); err != nil {
				return err
			}
		} else if agent == "kiro" {
			if err := kiro.Install(root); err != nil {
				return err
			}
		} else if agent == "kilo" {
			if err := kilo.Install(root); err != nil {
				return err
			}
		} else if agent == "amp" {
			if err := amp.Install(root); err != nil {
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
			result := map[string]any{"initialized": true, "repo": root}
			if agent == "gemini" {
				result["additional_setup"] = geminiEnableNotice
			} else if agent == "qwen" {
				result["additional_setup"] = qwenEnableNotice
			}
			return json.NewEncoder(c.OutOrStdout()).Encode(result)
		}
		if agent == "gemini" {
			_, err = fmt.Fprintf(c.OutOrStdout(), "Trace initialized. Gemini hooks installed. %s\n", geminiEnableNotice)
			return err
		}
		if agent == "qwen" {
			_, err = fmt.Fprintf(c.OutOrStdout(), "Trace initialized. Qwen hooks installed. %s\n", qwenEnableNotice)
			return err
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Trace initialized. %s turns will be recorded here.\n", agent)
		return err
	}}
	cmd.Flags().StringVar(&agent, "agent", "claude-code", "agent to integrate")
	cmd.Flags().BoolVar(&ifNeeded, "if-needed", false, "no-op if Trace is already initialized (safe for scripts/agents)")
	return cmd
}
