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
					cmd := exec.Command(pbin, args...)
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
