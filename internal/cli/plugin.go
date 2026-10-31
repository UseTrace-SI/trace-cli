package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// loadPlugins registers any executable named trace-<name> found on PATH as a
// subcommand, mirroring Entire's plugin model. Plugins receive the same args
// and environment as the parent command.
func loadPlugins(root *cobra.Command) {
	seen := map[string]bool{}
	for _, command := range root.Commands() {
		seen[command.Name()] = true
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasPrefix(name, "trace-") || name == "trace" {
				continue
			}
			pluginName := strings.TrimPrefix(name, "trace-")
			if pluginName == "" || seen[pluginName] {
				continue
			}
			bin := filepath.Join(dir, name)
			bin, err = filepath.Abs(bin)
			if err != nil {
				continue
			}
			// ensure executable
			info, err := e.Info()
			if err != nil || info.Mode()&0111 == 0 {
				continue
			}
			seen[pluginName] = true
			pname := pluginName
			pbin := bin
			cmd := &cobra.Command{
				Use:                pname,
				Short:              "plugin: " + pname,
				DisableFlagParsing: true,
				RunE: func(c *cobra.Command, args []string) error {
					target, _ := root.PersistentFlags().GetString("repo")
					args, target, err := pluginRepoArgs(args, target)
					if err != nil {
						return err
					}
					cmd := exec.Command(pbin, args...)
					if target != "" || pname == "brain" || pname == "graph" {
						dir, err := rootFromFlag(target)
						if err != nil && target != "" {
							return err
						}
						if err == nil {
							cmd.Dir = dir
						}
					}
					cmd.Stdin = c.InOrStdin()
					cmd.Stdout = c.OutOrStdout()
					cmd.Stderr = c.ErrOrStderr()
					if err := cmd.Run(); err != nil {
						return fmt.Errorf("plugin %s: %w", pname, err)
					}
					return nil
				},
			}
			root.AddCommand(cmd)
		}
	}
}

// Cobra leaves inherited flags in the argument list when a plugin disables
// flag parsing. Consume Trace's repository selector, preserving all plugin
// arguments and the literal argument boundary.
func pluginRepoArgs(args []string, target string) ([]string, string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out = append(out, args[i:]...)
			break
		}
		if arg == "--repo" {
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") {
				return nil, "", fmt.Errorf("--repo requires a directory")
			}
			i++
			target = args[i]
		} else if strings.HasPrefix(arg, "--repo=") {
			target = strings.TrimPrefix(arg, "--repo=")
		} else {
			out = append(out, arg)
		}
	}
	return out, target, nil
}
