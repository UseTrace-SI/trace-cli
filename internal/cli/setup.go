package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/amp"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/cline"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/codexhook"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/copilot"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/cursor"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/factory"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/gemini"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hermes"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/kilo"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/kimi"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/kiro"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/openclaude"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/opencode"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/openhands"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/pi"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/poolside"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/qwen"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/windsurf"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/zoo"
	"github.com/spf13/cobra"
)

var globalClaudeEvents = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "StopFailure", "SessionEnd"}

const geminiEnableNotice = "Ensure Gemini hooks are enabled; some versions require hooks.enabled=true in settings.json. This can enable other configured hooks."
const qwenEnableNotice = "Use a Qwen Code version that supports hooks and confirm they are enabled; version 0.0.14 does not emit Trace hooks."

var poolVersionPattern = regexp.MustCompile(`^(?:pool\s+)?v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+][0-9A-Za-z.-]+)?$`)

func poolsideSetupNotice() string {
	const instruction = "Poolside 1.0.16 or later is required; restart Poolside after setup."
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "pool", "--version").Output()
	if err != nil {
		return instruction
	}
	version := strings.TrimSpace(string(output))
	match := poolVersionPattern.FindStringSubmatch(version)
	if len(match) != 4 {
		return instruction
	}
	major, e1 := strconv.Atoi(match[1])
	minor, e2 := strconv.Atoi(match[2])
	patch, e3 := strconv.Atoi(match[3])
	if e1 != nil || e2 != nil || e3 != nil {
		return instruction
	}
	prerelease := strings.Contains(strings.SplitN(version, "+", 2)[0], "-")
	if major == 0 || (major == 1 && minor == 0 && (patch < 16 || (patch == 16 && prerelease))) {
		return fmt.Sprintf("Installed Poolside version %s is too old. %s", version, instruction)
	}
	return instruction
}

func setupAgent(home, name string, remove bool) error {
	switch name {
	case "copilot":
		if remove {
			return copilot.UninstallGlobal(home)
		}
		return copilot.InstallGlobal(home)
	case "windsurf":
		if remove {
			return windsurf.UninstallGlobal(home)
		}
		return windsurf.InstallGlobal(home)
	case "factory":
		if remove {
			return factory.UninstallGlobal(home)
		}
		return factory.InstallGlobal(home)
	case "opencode":
		return opencode.SetupGlobal(home, remove)
	case "kilo":
		return kilo.SetupGlobal(home, remove)
	case "pi", "omp":
		return pi.SetupGlobal(home, name, remove)
	case "amp":
		return amp.SetupGlobal(home, remove)
	case "qwen":
		if remove {
			return qwen.UninstallGlobal(home)
		}
		return qwen.InstallGlobal(home)
	case "gemini":
		if remove {
			return gemini.UninstallGlobal(home)
		}
		return gemini.InstallGlobal(home)
	case "cursor":
		if remove {
			return cursor.UninstallGlobal(home)
		}
		return cursor.InstallGlobal(home)
	case "zoo":
		return zoo.Setup(home, remove)
	case "hermes":
		return hermes.Setup(home, remove)
	case "poolside":
		return poolside.Setup(home, remove)
	case "cline":
		if remove {
			return cline.UninstallGlobal(home)
		}
		return cline.InstallGlobal(home)
	case "openhands":
		return openhands.Setup(home, remove)
	case "auggie":
		if remove {
			return hookconfig.Uninstall(home, name, ".augment/settings.json", true)
		}
		return hookconfig.Install(home, name, ".augment/settings.json", []string{"SessionStart", "PromptSubmit", "PostToolUse", "Stop", "SessionEnd"}, true)
	case "claude-code":
		root, relative := home, ".claude/settings.json"
		if config := os.Getenv("CLAUDE_CONFIG_DIR"); config != "" {
			if !filepath.IsAbs(config) {
				return fmt.Errorf("CLAUDE_CONFIG_DIR must be absolute")
			}
			root, relative = config, "settings.json"
		}
		if remove {
			return hookconfig.Uninstall(root, name, relative, true)
		}
		return hookconfig.Install(root, name, relative, globalClaudeEvents, true)
	case "codex":
		if remove {
			return codexhook.Uninstall(home)
		}
		return codexhook.Install(home)
	case "kiro":
		if remove {
			return kiro.Uninstall(home)
		}
		return kiro.InstallGlobal(home)
	case "kimi":
		if remove {
			return kimi.Uninstall(home)
		}
		return kimi.Install(home)
	case "openclaude":
		configRoot := os.Getenv("OPENCLAUDE_CONFIG_DIR")
		if configRoot == "" {
			configRoot = filepath.Join(home, ".openclaude")
		}
		var err error
		configRoot, err = filepath.Abs(configRoot)
		if err != nil {
			return fmt.Errorf("resolving OpenClaude config directory: %w", err)
		}
		if remove {
			return openclaude.UninstallGlobal(configRoot)
		}
		if err := os.MkdirAll(filepath.Join(configRoot, ".trace"), 0700); err != nil {
			return fmt.Errorf("creating OpenClaude setup state: %w", err)
		}
		return openclaude.InstallGlobal(configRoot)
	default:
		return fmt.Errorf("global setup is not available for agent %q", name)
	}
}

func setupCommand(jsonOutput *bool) *cobra.Command {
	var agents []string
	var remove bool
	cmd := &cobra.Command{Use: "setup", Short: "Enable automatic recording across repositories", RunE: func(c *cobra.Command, _ []string) error {
		if len(agents) == 0 {
			agents = []string{"claude-code", "codex"}
		}
		for _, name := range agents {
			if name != "copilot" && name != "windsurf" && name != "factory" && name != "claude-code" && name != "codex" && name != "openclaude" && name != "kiro" && name != "kimi" && name != "auggie" && name != "openhands" && name != "cline" && name != "poolside" && name != "hermes" && name != "zoo" && name != "cursor" && name != "gemini" && name != "qwen" && name != "amp" && name != "pi" && name != "omp" && name != "kilo" && name != "opencode" {
				return fmt.Errorf("global setup is not available for agent %q", name)
			}
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("finding home directory: %w", err)
		}
		for _, name := range agents {
			if !remove && (name == "claude-code" || name == "codex" || name == "auggie") {
				if err := os.MkdirAll(filepath.Join(home, ".trace"), 0700); err != nil {
					return fmt.Errorf("creating setup state directory: %w", err)
				}
			}
			if err := setupAgent(home, name, remove); err != nil {
				return fmt.Errorf("setting up %s: %w", name, err)
			}
		}
		if *jsonOutput {
			result := map[string]any{"agents": agents, "enabled": !remove}
			var additional []string
			for _, name := range agents {
				if name == "copilot" && !remove {
					result["enabled"] = false
					additional = append(additional, "Restart Copilot CLI and confirm hooks are active.")
				}
				if name == "windsurf" && !remove {
					result["enabled"] = false
					additional = append(additional, "Restart Windsurf or Devin Desktop and confirm hooks are active.")
				}
				if name == "codex" && !remove {
					result["enabled"] = false
					additional = append(additional, "Review and trust the Codex hooks with /hooks; ensure hooks are enabled.")
				}
				if name == "gemini" && !remove {
					result["enabled"] = false
					additional = append(additional, geminiEnableNotice)
				}
				if name == "qwen" && !remove {
					result["enabled"] = false
					additional = append(additional, qwenEnableNotice)
				}
				if name == "opencode" && !remove {
					result["enabled"] = false
					additional = append(additional, "Restart OpenCode and confirm the Trace plugin loaded.")
				}
				if name == "kilo" && !remove {
					result["enabled"] = false
					additional = append(additional, "Restart Kilo and confirm the Trace plugin loaded.")
				}
				if (name == "pi" || name == "omp") && !remove {
					result["enabled"] = false
					additional = append(additional, "Restart "+name+" and confirm the Trace extension loaded.")
				}
				if name == "amp" && !remove {
					result["enabled"] = false
					additional = append(additional, "Reload Amp plugins and check activation with amp plugins list.")
				}
				if name == "zoo" {
					result["enabled"] = false
					if remove {
						additional = append(additional, "Uninstall the Trace Zoo Code Observer extension in your editor.")
					} else {
						additional = append(additional, "Install the generated VSIX in your editor and reload the window.")
						result["extension_package"] = zoo.Path(home)
					}
				}
				if name == "hermes" && !remove {
					result["enabled"] = false
					additional = append(additional, "Enable trace-observer in Hermes and set TRACE_HERMES_REPO.")
				}
				if name == "poolside" && !remove {
					result["enabled"] = false
					additional = append(additional, poolsideSetupNotice())
				}
				if name == "kiro" && !remove {
					result["enabled"] = false
					additional = append(additional, "Restart Kiro and confirm hooks are active.")
				}
			}
			if len(additional) > 0 {
				result["additional_setup"] = strings.Join(additional, " ")
			}
			return json.NewEncoder(c.OutOrStdout()).Encode(result)
		}
		if remove {
			message := "Global Trace hooks removed."
			for _, name := range agents {
				if name == "zoo" {
					message += " Uninstall the Trace Zoo Code Observer extension in your editor."
				}
			}
			_, err = fmt.Fprintln(c.OutOrStdout(), message)
			return err
		}
		message := "Automatic recording enabled."
		for _, name := range agents {
			if name == "codex" || name == "poolside" || name == "kiro" || name == "gemini" || name == "qwen" {
				message = "Selected integrations installed."
				break
			}
		}
		if len(agents) == 1 && agents[0] == "hermes" {
			message = "Hermes observer installed."
		}
		if len(agents) == 1 && agents[0] == "zoo" {
			message = "Zoo Code observer package created."
		}
		if len(agents) == 1 && agents[0] == "amp" {
			message = "Amp observer installed."
		}
		if len(agents) == 1 && agents[0] == "kilo" {
			message = "Kilo observer installed."
		}
		if len(agents) == 1 && agents[0] == "opencode" {
			message = "OpenCode observers installed."
		}
		if len(agents) == 1 && agents[0] == "windsurf" {
			message = "Windsurf hooks installed."
		}
		if len(agents) == 1 && agents[0] == "copilot" {
			message = "Copilot CLI hooks installed."
		}
		if len(agents) == 1 && agents[0] == "poolside" {
			message = "Poolside hooks installed."
		}
		if len(agents) == 1 && agents[0] == "kiro" {
			message = "Kiro hooks installed."
		}
		if len(agents) == 1 && (agents[0] == "pi" || agents[0] == "omp") {
			message = agents[0] + " observer installed."
		}
		for _, name := range agents {
			if name == "gemini" {
				message += " " + geminiEnableNotice
			}
			if name == "qwen" {
				message += " " + qwenEnableNotice
			}
			if name == "copilot" {
				message += " Restart Copilot CLI and confirm hooks are active."
			}
			if name == "poolside" {
				message += " " + poolsideSetupNotice()
			}
			if name == "kiro" {
				message += " Restart Kiro and confirm hooks are active."
			}
			if name == "windsurf" {
				message += " Restart Windsurf or Devin Desktop and confirm hooks are active."
			}
			if name == "opencode" {
				message += " Restart OpenCode and confirm the Trace plugin loaded."
			}
			if name == "kilo" {
				message += " Restart Kilo and confirm the Trace plugin loaded."
			}
			if name == "pi" || name == "omp" {
				message += " Restart " + name + " and confirm the Trace extension loaded."
			}
			if name == "codex" {
				message += " In Codex, review and trust the new hooks with /hooks."
			}
			if name == "hermes" {
				message += " Enable trace-observer in Hermes and set TRACE_HERMES_REPO to the repository path."
			}
			if name == "zoo" {
				message += " Install " + zoo.Path(home) + " as a VSIX in your editor and reload the window."
			}
			if name == "amp" {
				message += " Reload Amp plugins and check activation with amp plugins list."
			}
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), message)
		return err
	}}
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "agents to enable (claude-code, codex, cursor, windsurf, copilot, gemini, qwen, factory, amp, pi, omp, kilo, opencode, openclaude, kiro, kimi, auggie, openhands, cline, poolside, hermes, zoo)")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove selected global hooks")
	return cmd
}
