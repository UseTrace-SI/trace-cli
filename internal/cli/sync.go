package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
	"github.com/spf13/cobra"
)

var remoteName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func loginPath(root string) string { return filepath.Join(repo.TraceDir(root), "login.json") }

func syncTokenProvider(cfg syncclient.Config) (func(context.Context) (string, error), error) {
	credentials, err := syncclient.LoadCredentials(cfg.Remote)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) {
		if credentials.ExpiresAt == 0 || credentials.ExpiresAt > time.Now().Add(time.Minute).UnixMilli() {
			return credentials.AccessToken, nil
		}
		if !cfg.GitHubApp {
			return "", errors.New("GitHub login expired; run trace login again")
		}
		updated, err := (syncclient.OAuth{App: true}).Refresh(ctx, cfg.ClientID, credentials)
		if err != nil {
			return "", err
		}
		if err := syncclient.SaveCredentials(cfg.Remote, updated); err != nil {
			return "", err
		}
		credentials = updated
		return credentials.AccessToken, nil
	}, nil
}

func loginCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var remote, mode, endpoint, clientID string
	var githubApp bool
	cmd := &cobra.Command{Use: "login", Short: "Connect optional sync with GitHub", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if !remoteName.MatchString(remote) {
			return errors.New("--remote must be owner/repo")
		}
		cfg := syncclient.Config{Endpoint: endpoint, Remote: remote, Mode: mode, ClientID: clientID, GitHubApp: githubApp}
		if err := cfg.Validate(); err != nil {
			return err
		}
		if clientID == "" {
			return errors.New("--client-id or TRACE_GITHUB_CLIENT_ID is required")
		}
		ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
		defer stop()
		oauth := syncclient.OAuth{App: githubApp}
		code, err := oauth.Start(ctx, clientID)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.ErrOrStderr(), "Open %s and enter code %s\n", code.VerifyURL, code.UserCode); err != nil {
			return err
		}
		credentials, err := oauth.WaitCredentials(ctx, clientID, code)
		if err != nil {
			return err
		}
		if err := syncclient.SaveCredentials(remote, credentials); err != nil {
			return err
		}
		b, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if err := os.WriteFile(loginPath(root), b, 0600); err != nil {
			return fmt.Errorf("saving sync configuration: %w", err)
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"logged_in": true, "remote": remote, "mode": mode})
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Sync login saved for %s. Run trace sync when ready.\n", remote)
		return err
	}}
	cmd.Flags().StringVar(&remote, "remote", "", "GitHub owner/repo")
	cmd.Flags().StringVar(&mode, "mode", "pro", "sync mode: pro or team")
	cmd.Flags().StringVar(&endpoint, "endpoint", os.Getenv("TRACE_SYNC_URL"), "Trace sync server HTTPS URL")
	cmd.Flags().StringVar(&clientID, "client-id", os.Getenv("TRACE_GITHUB_CLIENT_ID"), "GitHub client ID")
	cmd.Flags().BoolVar(&githubApp, "github-app", false, "use fine-grained GitHub App authorization")
	return cmd
}

func syncCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var watch bool
	cmd := &cobra.Command{Use: "sync", Short: "Upload local history after login", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(loginPath(root))
		if os.IsNotExist(err) {
			return errors.New("sync is off; run trace login first")
		}
		if err != nil {
			return err
		}
		var cfg syncclient.Config
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("reading sync configuration: %w", err)
		}
		tokenProvider, err := syncTokenProvider(cfg)
		if err != nil {
			return err
		}
		token, err := tokenProvider(c.Context())
		if err != nil {
			return err
		}
		e, err := engine.Open(root)
		if err != nil {
			return err
		}
		defer e.Close()
		client := &syncclient.Client{Config: cfg, Token: token, TraceDir: repo.TraceDir(root), DB: e.DB.DB, Blobs: e.Blobs}
		if cfg.GitHubApp {
			client.TokenProvider = tokenProvider
		}
		if cfg.Mode == "pro" {
			passphrase := os.Getenv("TRACE_SYNC_PASSPHRASE")
			if passphrase == "" {
				return errors.New("pro sync requires TRACE_SYNC_PASSPHRASE")
			}
			client.Cipher, err = syncclient.LoadCipher(client.TraceDir, passphrase)
			if err != nil {
				return err
			}
		}
		ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
		defer stop()
		for {
			client.Token, err = tokenProvider(ctx)
			if err != nil {
				return err
			}
			blobs, rows, err := client.Sync(ctx)
			if err != nil {
				return err
			}
			if *jsonOutput {
				if err := json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"blobs": blobs, "rows": rows}); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintf(c.OutOrStdout(), "Synced %d blobs and %d rows.\n", blobs, rows); err != nil {
				return err
			}
			if !watch {
				return nil
			}
			timer := time.NewTimer(15 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}}
	cmd.Flags().BoolVar(&watch, "watch", false, "sync every 15 seconds until interrupted")
	cmd.AddCommand(pullCommand(repoPath, jsonOutput))
	return cmd
}

func pullCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var into string
	cmd := &cobra.Command{Use: "pull", Short: "Recover synced history into a fresh Git checkout", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if into == "" {
			return errors.New("--into must name an existing Git checkout")
		}
		b, err := os.ReadFile(loginPath(root))
		if err != nil {
			return fmt.Errorf("reading sync login: %w", err)
		}
		var cfg syncclient.Config
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("decoding sync login: %w", err)
		}
		tokenProvider, err := syncTokenProvider(cfg)
		if err != nil {
			return err
		}
		token, err := tokenProvider(c.Context())
		if err != nil {
			return err
		}
		target, err := repo.FindRoot(into)
		if err != nil {
			return err
		}
		if _, err := os.Stat(repo.TraceDir(target)); err == nil {
			return errors.New("target already has .trace history; choose a fresh checkout")
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("checking target history: %w", err)
		}
		if err := addExclude(target); err != nil {
			return err
		}
		client := &syncclient.Client{Config: cfg, Token: token}
		if cfg.GitHubApp {
			client.TokenProvider = tokenProvider
		}
		ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
		defer stop()
		blobs, rows, err := client.Pull(ctx, repo.TraceDir(target), os.Getenv("TRACE_SYNC_PASSPHRASE"))
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"blobs": blobs, "rows": rows, "repo": target})
		}
		_, err = fmt.Fprintf(c.OutOrStdout(), "Recovered %d blobs and %d rows into %s. Use trace --repo %s log to inspect history.\n", blobs, rows, target, target)
		return err
	}}
	cmd.Flags().StringVar(&into, "into", "", "fresh Git checkout to receive downloaded history")
	return cmd
}
