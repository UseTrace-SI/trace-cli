package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/spf13/cobra"
)

func agentCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "agent"}
	add := &cobra.Command{Use: "add <name>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if _, err := repo.FindRoot(root); err != nil {
			return err
		}
		switch args[0] {
		case "codebuff":
			if err := codebuff.Install(root); err != nil {
				return err
			}
		case "aider":
			if err := aider.Install(root); err != nil {
				return err
			}
		case "crewai":
			if err := crewai.Install(root); err != nil {
				return err
			}
		case "claude-code":
			if err := claudecode.Install(root); err != nil {
				return err
			}
		case "cline":
			if err := cline.Install(root); err != nil {
				return err
			}
		case "openclaude":
			if err := openclaude.Install(root); err != nil {
				return err
			}
		case "openhands":
			if err := openhands.InstallProject(root); err != nil {
				return err
			}
		case "cursor":
			if err := cursor.Install(root); err != nil {
				return err
			}
		case "windsurf":
			if err := windsurf.Install(root); err != nil {
				return err
			}
		case "gemini":
			if err := gemini.Install(root); err != nil {
				return err
			}
		case "copilot":
			if err := copilot.Install(root); err != nil {
				return err
			}
		case "qwen":
			if err := qwen.Install(root); err != nil {
				return err
			}
		case "factory":
			if err := factory.Install(root); err != nil {
				return err
			}
		case "pi":
			if err := pi.Install(root); err != nil {
				return err
			}
		case "omp":
			if err := omp.Install(root); err != nil {
				return err
			}
		case "grok":
			if err := grok.Install(root); err != nil {
				return err
			}
		case "goose":
			if err := goose.Install(root); err != nil {
				return err
			}
		case "amp":
			if err := amp.Install(root); err != nil {
				return err
			}
		case "kilo":
			if err := kilo.Install(root); err != nil {
				return err
			}
		case "kiro":
			if err := kiro.Install(root); err != nil {
				return err
			}
		case "langgraph":
			if err := langgraph.Install(root); err != nil {
				return err
			}
		case "agy":
			if err := agy.Install(root); err != nil {
				return err
			}
		case "codex":
			if err := osWriteEnabled(root, "codex.enabled"); err != nil {
				return err
			}
		case "opencode":
			if err := osWriteEnabled(root, "opencode.enabled"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported agent %q", args[0])
		}
		if *jsonOutput {
			result := map[string]any{"agent": args[0], "added": true}
			if args[0] == "gemini" {
				result["additional_setup"] = geminiEnableNotice
			} else if args[0] == "qwen" {
				result["additional_setup"] = qwenEnableNotice
			}
			return json.NewEncoder(c.OutOrStdout()).Encode(result)
		}
		if args[0] == "gemini" {
			_, err = fmt.Fprintf(c.OutOrStdout(), "Added agent gemini. %s\n", geminiEnableNotice)
			return err
		}
		if args[0] == "qwen" {
			_, err = fmt.Fprintf(c.OutOrStdout(), "Added agent qwen. %s\n", qwenEnableNotice)
			return err
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Added agent %s.\n", args[0])
		return err
	}}
	cmd.AddCommand(add)
	remove := &cobra.Command{Use: "remove <name>", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if _, err := repo.FindRoot(root); err != nil {
			return err
		}
		name := args[0]
		switch name {
		case "codebuff":
			err = codebuff.Uninstall(root)
		case "aider":
			err = aider.Uninstall(root)
		case "crewai":
			err = crewai.Uninstall(root)
		case "claude-code":
			err = claudecode.Uninstall(root)
		case "cline":
			err = cline.Uninstall(root)
		case "openclaude":
			err = openclaude.Uninstall(root)
		case "openhands":
			err = openhands.UninstallProject(root)
		case "codex", "opencode":
			err = os.Remove(filepath.Join(repo.TraceDir(root), name+".enabled"))
			if os.IsNotExist(err) {
				err = nil
			}
		case "cursor":
			err = cursor.Uninstall(root)
		case "windsurf":
			err = windsurf.Uninstall(root)
		case "gemini":
			err = gemini.Uninstall(root)
		case "copilot":
			err = copilot.Uninstall(root)
		case "qwen":
			err = qwen.Uninstall(root)
		case "factory":
			err = factory.Uninstall(root)
		case "pi":
			err = pi.Uninstall(root)
		case "omp":
			err = omp.Uninstall(root)
		case "grok":
			err = grok.Uninstall(root)
		case "goose":
			err = goose.Uninstall(root)
		case "amp":
			err = amp.Uninstall(root)
		case "kilo":
			err = kilo.Uninstall(root)
		case "kiro":
			err = kiro.Uninstall(root)
		case "langgraph":
			err = langgraph.Uninstall(root)
		case "agy":
			err = agy.Uninstall(root)
		default:
			return fmt.Errorf("unsupported agent %q", name)
		}
		if err != nil {
			return fmt.Errorf("removing agent %s: %w", name, err)
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"agent": name, "removed": true})
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Removed agent %s.\n", name)
		return err
	}}
	cmd.AddCommand(remove)
	return cmd
}

func osWriteEnabled(root, name string) error {
	dir := repo.TraceDir(root)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte("1\n"), 0600)
}
