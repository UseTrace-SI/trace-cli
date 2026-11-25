package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters"
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
		agent, ok := adapters.ForProject(args[0])
		if !ok {
			return fmt.Errorf("unsupported agent %q", args[0])
		}
		if err := addProjectAgent(agent, root); err != nil {
			return err
		}
		var notice string
		if n, ok := agent.(adapters.ProjectNoticer); ok {
			_, notice = n.ProjectNotice()
		}
		if *jsonOutput {
			result := map[string]any{"agent": args[0], "added": true}
			if notice != "" {
				result["additional_setup"] = notice
			}
			return json.NewEncoder(c.OutOrStdout()).Encode(result)
		}
		if notice != "" {
			_, err = fmt.Fprintf(c.OutOrStdout(), "Added agent %s. %s\n", args[0], notice)
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
		agent, ok := adapters.ForProject(name)
		if !ok {
			return fmt.Errorf("unsupported agent %q", name)
		}
		if err := removeProjectAgent(agent, root); err != nil {
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

// addProjectAgent installs agent's hooks, or writes its marker, in root.
func addProjectAgent(agent adapters.Adapter, root string) error {
	if m, ok := agent.(adapters.MarkerEnabled); ok {
		return osWriteEnabled(root, m.EnableMarker().File)
	}
	return agent.(adapters.ProjectInstaller).InstallProject(root)
}

func removeProjectAgent(agent adapters.Adapter, root string) error {
	if m, ok := agent.(adapters.MarkerEnabled); ok {
		err := os.Remove(filepath.Join(repo.TraceDir(root), m.EnableMarker().File))
		if os.IsNotExist(err) {
			err = nil
		}
		return err
	}
	return agent.(adapters.ProjectInstaller).RemoveProject(root)
}

func osWriteEnabled(root, name string) error {
	dir := repo.TraceDir(root)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte("1\n"), 0600)
}
