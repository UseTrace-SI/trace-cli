package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/companions"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
	"github.com/spf13/cobra"
)

func connectCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var remote, endpoint, device string
	var noBrowser bool
	cmd := &cobra.Command{Use: "connect", Short: "Connect this repository to your private hosted dashboard", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if remote == "" {
			remote, err = discoverRemote(c.Context(), root)
			if err != nil {
				remote = "local/" + filepath.Base(root)
			}
		}
		if !remoteName.MatchString(remote) {
			return fmt.Errorf("--remote must be a project name in owner/repo form")
		}
		remote = strings.ToLower(remote)
		cfg := syncclient.Config{Endpoint: endpoint, Remote: remote, Mode: "personal", AutoSync: true}
		if device == "" {
			device, _ = os.Hostname()
		}
		if device == "" {
			device = "My computer"
		}
		ctx, cancel := context.WithTimeout(c.Context(), 10*time.Minute)
		defer cancel()
		flow, err := syncclient.StartPairing(ctx, nil, cfg, device)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.ErrOrStderr(), "Approve this repository in your Trace dashboard:\n%s\n", flow.VerificationURI); err != nil {
			return err
		}
		if !noBrowser {
			_ = openLoginBrowser(flow.VerificationURI)
		}
		credentials, err := syncclient.WaitPairing(ctx, nil, endpoint, flow)
		if err != nil {
			return err
		}
		cfg.ConnectionID = credentials.ConnectionID
		cfg.ConnectionExpiresAt = credentials.ExpiresAt
		if err := syncclient.SaveCredentials(cfg.CredentialKey(), credentials); err != nil {
			return err
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if err := os.WriteFile(loginPath(root), data, 0600); err != nil {
			return fmt.Errorf("saving dashboard connection: %w", err)
		}
		report := companions.Refresh(ctx, root)
		if err := companions.SaveStatus(root, report); err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		err = saveCompanionSnapshot(ctx, e)
		closeErr := e.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err := daemon.EnsureRunning(root); err != nil {
			return fmt.Errorf("starting background sync: %w", err)
		}
		if !daemon.PersonalSyncReady(filepath.Join(root, ".trace")) {
			return fmt.Errorf("connection saved, but the running daemon predates personal sync; run trace daemon stop, trace daemon start, then trace sync")
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"connected": true, "remote": remote, "mode": "personal", "dashboard": strings.TrimRight(endpoint, "/") + "/app/"})
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Connected %s to %s/app/. Existing history and new turns sync in the background.\n", remote, strings.TrimRight(endpoint, "/"))
		return err
	}}
	cmd.Flags().StringVar(&remote, "remote", "", "project name, such as owner/repo (defaults to GitHub remote)")
	cmd.Flags().StringVar(&endpoint, "endpoint", defaultSyncEndpoint, "Trace dashboard HTTPS URL")
	cmd.Flags().StringVar(&device, "device", "", "device name shown when approving the connection")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the approval URL without opening a browser")
	return cmd
}
