package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/plugins"
	"github.com/UseTrace-SI/trace-cli/internal/userstate"
	"github.com/spf13/cobra"
)

func localPluginManager(create bool) (plugins.Manager, error) {
	path, err := userstate.Path("plugins")
	if err != nil {
		return plugins.Manager{}, err
	}
	parent := filepath.Dir(path)
	if create {
		if err := os.MkdirAll(parent, 0700); err != nil {
			return plugins.Manager{}, err
		}
	}
	canonical, err := filepath.EvalSymlinks(parent)
	if err != nil && !os.IsNotExist(err) {
		return plugins.Manager{}, err
	}
	if err == nil {
		path = filepath.Join(canonical, "plugins")
	}
	return plugins.Manager{Root: path}, nil
}
func pluginManagerCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	parent := &cobra.Command{Use: "plugin", Short: "Verify and manage explicitly trusted offline plugin bundles"}
	report := func(cmd *cobra.Command, data any, text string) error {
		if *jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(data)
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), text)
		return err
	}
	var publisher, keyPath string
	var replace bool
	trust := &cobra.Command{Use: "trust", Args: cobra.NoArgs, Short: "Pin a publisher's Ed25519 PEM public key from a trusted local file", RunE: func(cmd *cobra.Command, _ []string) error {
		if publisher == "" || keyPath == "" {
			return fmt.Errorf("--publisher and --key are required")
		}
		absolute, err := filepath.Abs(keyPath)
		if err != nil {
			return err
		}
		root, err := os.OpenRoot(filepath.Dir(absolute))
		if err != nil {
			return err
		}
		defer root.Close()
		file, err := openCloudFile(root, filepath.Base(absolute))
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil || info.Size() > 1024 {
			file.Close()
			return fmt.Errorf("publisher public key exceeds budget")
		}
		data, err := io.ReadAll(io.LimitReader(file, 1025))
		file.Close()
		if err != nil {
			return err
		}
		key, err := plugins.DecodePublicKey(data)
		if err != nil {
			return err
		}
		manager, err := localPluginManager(true)
		if err != nil {
			return err
		}
		fingerprint, err := manager.Trust(publisher, key, replace)
		if err != nil {
			return err
		}
		return report(cmd, map[string]any{"publisher": publisher, "fingerprint": fingerprint, "trusted": true}, "Trusted publisher "+publisher+" · SHA-256 "+fingerprint)
	}}
	trust.Flags().StringVar(&publisher, "publisher", "", "publisher identity")
	trust.Flags().StringVar(&keyPath, "key", "", "PEM PUBLIC KEY file from a trusted channel")
	trust.Flags().BoolVar(&replace, "replace", false, "explicitly revoke a different previous key for this publisher")
	var downgrade bool
	install := &cobra.Command{Use: "install BUNDLE_DIRECTORY", Args: cobra.ExactArgs(1), Short: "Verify and activate a signed local bundle without running it", RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := localPluginManager(true)
		if err != nil {
			return err
		}
		receipt, err := manager.Install(args[0], downgrade)
		if err != nil {
			return err
		}
		return report(cmd, map[string]any{"receipt": receipt, "verified": true, "executed": false, "network": false}, "Installed "+receipt.Manifest.Name+" "+receipt.Manifest.Version+" from "+receipt.Manifest.Publisher+". No plugin was executed.")
	}}
	install.Flags().BoolVar(&downgrade, "allow-downgrade", false, "explicitly activate an older, signed version")
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List managed plugins and recheck signatures, trust and installed bytes", RunE: func(cmd *cobra.Command, _ []string) error {
		manager, err := localPluginManager(false)
		if err != nil {
			return err
		}
		items, err := manager.List()
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(items)
		}
		for _, item := range items {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s publisher=%s verified=%t\n", item.Receipt.Manifest.Name, item.Receipt.Manifest.Version, item.Receipt.Manifest.Publisher, item.Verified); err != nil {
				return err
			}
		}
		return nil
	}}
	run := &cobra.Command{Use: "run NAME -- [ARGS...]", Args: cobra.MinimumNArgs(1), Short: "Reverify and explicitly run a trusted host plugin", RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := localPluginManager(false)
		if err != nil {
			return err
		}
		path, err := manager.Executable(args[0])
		if err != nil {
			return err
		}
		process := exec.CommandContext(cmd.Context(), path, args[1:]...)
		if *repoPath != "" {
			root, err := rootFromFlag(*repoPath)
			if err != nil {
				return err
			}
			process.Dir = root
		}
		process.Stdin = cmd.InOrStdin()
		process.Stdout = cmd.OutOrStdout()
		process.Stderr = cmd.ErrOrStderr()
		if err := process.Run(); err != nil {
			return fmt.Errorf("managed plugin failed: %w", err)
		}
		return nil
	}}
	remove := &cobra.Command{Use: "remove NAME", Args: cobra.ExactArgs(1), Short: "Deactivate a managed plugin while retaining signed release files", RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := localPluginManager(false)
		if err != nil {
			return err
		}
		if err := manager.Remove(args[0]); err != nil {
			return err
		}
		return report(cmd, map[string]any{"name": args[0], "removed": true, "artifacts_retained": true}, "Deactivated "+args[0]+"; signed release files remain local.")
	}}
	untrust := &cobra.Command{Use: "untrust PUBLISHER", Args: cobra.ExactArgs(1), Short: "Revoke a publisher key; its plugins fail future verification", RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := localPluginManager(false)
		if err != nil {
			return err
		}
		if err := manager.Untrust(args[0]); err != nil {
			return err
		}
		return report(cmd, map[string]any{"publisher": args[0], "trusted": false}, "Revoked publisher "+args[0]+".")
	}}
	prune := &cobra.Command{Use: "prune NAME", Args: cobra.ExactArgs(1), Short: "Remove marked inactive releases, preserving the active and previous versions", RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := localPluginManager(false)
		if err != nil {
			return err
		}
		count, err := manager.Prune(args[0])
		if err != nil {
			return err
		}
		return report(cmd, map[string]any{"name": args[0], "pruned": count}, fmt.Sprintf("Pruned %d inactive releases.", count))
	}}
	parent.AddCommand(trust, install, list, run, remove, untrust, prune)
	return parent
}
