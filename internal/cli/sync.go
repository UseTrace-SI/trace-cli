package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
	"github.com/spf13/cobra"
)

var remoteName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

const defaultSyncEndpoint = "https://usetrace.si"

func githubRemote(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	var path string
	if strings.HasPrefix(raw, "git@github.com:") {
		path = strings.TrimPrefix(raw, "git@github.com:")
	} else {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "ssh") || !strings.EqualFold(u.Hostname(), "github.com") || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || (u.User != nil && (u.Scheme != "ssh" || u.User.String() != "git")) {
			return "", errors.New("origin is not a supported GitHub URL; use --remote owner/repo")
		}
		path = strings.TrimPrefix(u.Path, "/")
	}
	path = strings.TrimSuffix(path, ".git")
	if !remoteName.MatchString(path) {
		return "", errors.New("origin is not a GitHub repository; use --remote owner/repo")
	}
	return path, nil
}

func discoverRemote(ctx context.Context, root string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("finding GitHub origin; use --remote owner/repo: %w", err)
	}
	return githubRemote(string(output))
}

func discoverLoginConfig(ctx context.Context, endpoint string, client *http.Client) (string, bool, error) {
	cfg := syncclient.Config{Endpoint: endpoint, Remote: "a/b", Mode: "pro"}
	if err := cfg.Validate(); err != nil {
		return "", false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/api/config", nil)
	if err != nil {
		return "", false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("loading Trace login settings: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("loading Trace login settings: server returned %s", resp.Status)
	}
	var settings struct {
		ClientID string `json:"clientId"`
		AuthMode string `json:"authMode"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&settings); err != nil {
		return "", false, fmt.Errorf("reading Trace login settings: %w", err)
	}
	if settings.ClientID == "" {
		return "", false, errors.New("GitHub sign-in is not configured on this Trace server yet")
	}
	if settings.AuthMode != "app" && settings.AuthMode != "oauth" {
		return "", false, errors.New("Trace server returned an unsupported authentication mode")
	}
	return settings.ClientID, settings.AuthMode == "app", nil
}

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
		updated.ProSyncKey = credentials.ProSyncKey
		if err := syncclient.SaveCredentials(cfg.Remote, updated); err != nil {
			return "", err
		}
		credentials = updated
		return credentials.AccessToken, nil
	}, nil
}

func configuredCipher(traceDir string, cfg syncclient.Config) (*syncclient.Cipher, error) {
	if passphrase := os.Getenv("TRACE_SYNC_PASSPHRASE"); passphrase != "" {
		return syncclient.LoadCipher(traceDir, passphrase)
	}
	credentials, err := syncclient.LoadCredentials(cfg.Remote)
	if err != nil {
		return nil, err
	}
	salt, err := os.ReadFile(filepath.Join(traceDir, "sync-salt"))
	if err != nil {
		return nil, fmt.Errorf("reading Pro sync salt: %w", err)
	}
	return syncclient.FromKey(credentials.ProSyncKey, salt)
}

func browserCommand(goos, target string) *exec.Cmd {
	switch goos {
	case "darwin":
		return exec.Command("open", target)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		return exec.Command("xdg-open", target)
	}
}

func openLoginBrowser(target string) bool {
	cmd := browserCommand(runtime.GOOS, target)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return false
	}
	go func() { _ = cmd.Wait() }()
	return true
}

// autoSyncLoop uploads acknowledged local history without holding up hook delivery.
func autoSyncLoop(ctx context.Context, root string, e *engine.Engine, logger *slog.Logger, wake <-chan struct{}) {
	path := loginPath(root)
	const retryBase = 15 * time.Second
	const retryMax = 5 * time.Minute
	const idleDelay = 5 * time.Minute
	const configCheckDelay = 15 * time.Second
	retryDelay := retryBase
	timer := time.NewTimer(0)
	defer timer.Stop()
	reset := func(delay time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			reset(0)
			continue
		case <-timer.C:
		}
		b, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				logger.Warn("automatic sync configuration unavailable", "error", err)
			}
			reset(configCheckDelay)
			continue
		}
		var cfg syncclient.Config
		if err := json.Unmarshal(b, &cfg); err != nil {
			logger.Warn("automatic sync configuration invalid", "error", err)
			reset(configCheckDelay)
			continue
		}
		if !cfg.AutoSync {
			retryDelay = retryBase
			reset(configCheckDelay)
			continue
		}
		tokenProvider, err := syncTokenProvider(cfg)
		if err != nil {
			logger.Warn("automatic sync unavailable", "error", err)
			reset(retryDelay)
			retryDelay = min(retryDelay*2, retryMax)
			continue
		}
		token, err := tokenProvider(ctx)
		if err != nil {
			logger.Warn("automatic sync login unavailable", "error", err)
			reset(retryDelay)
			retryDelay = min(retryDelay*2, retryMax)
			continue
		}
		client := &syncclient.Client{Config: cfg, Token: token, TokenProvider: tokenProvider,
			TraceDir: repo.TraceDir(root), DB: e.DB.DB, Blobs: e.Blobs}
		if cfg.Mode == "pro" {
			client.Cipher, err = configuredCipher(client.TraceDir, cfg)
			if err != nil {
				logger.Warn("automatic Pro sync unavailable", "error", err)
				reset(retryDelay)
				retryDelay = min(retryDelay*2, retryMax)
				continue
			}
		}
		blobs, rows, err := client.Sync(ctx)
		if err != nil {
			logger.Warn("automatic sync failed; will retry", "error", err)
			reset(retryDelay)
			retryDelay = min(retryDelay*2, retryMax)
			continue
		}
		retryDelay = retryBase
		if blobs > 0 || rows > 0 {
			logger.Info("automatic sync complete", "blobs", blobs, "rows", rows)
			reset(retryBase)
		} else {
			reset(idleDelay)
		}
	}
}

func loginCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var remote, mode, endpoint, clientID string
	var githubApp bool
	var autoSync bool
	var noBrowser bool
	cmd := &cobra.Command{Use: "login", Short: "Connect optional sync with GitHub", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if remote == "" {
			remote, err = discoverRemote(c.Context(), root)
			if err != nil {
				return err
			}
		}
		if !remoteName.MatchString(remote) {
			return errors.New("--remote must be owner/repo")
		}
		if autoSync && mode == "pro" && os.Getenv("TRACE_SYNC_PASSPHRASE") == "" {
			return errors.New("automatic Pro sync requires TRACE_SYNC_PASSPHRASE during login so its key can be saved in the OS credential store")
		}
		if clientID == "" {
			httpClient := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			discoveredID, app, err := discoverLoginConfig(c.Context(), endpoint, httpClient)
			if err != nil {
				return err
			}
			clientID = discoveredID
			if !c.Flags().Changed("github-app") {
				githubApp = app
			}
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
		opened := false
		if !noBrowser {
			opened = openLoginBrowser(code.VerifyURL)
		}
		if opened {
			if _, err := fmt.Fprintf(c.ErrOrStderr(), "Browser opened for GitHub authorization. If needed, visit %s and enter code %s.\n", code.VerifyURL, code.UserCode); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintf(c.ErrOrStderr(), "Open %s and enter code %s\n", code.VerifyURL, code.UserCode); err != nil {
			return err
		}
		credentials, err := oauth.WaitCredentials(ctx, clientID, code)
		if err != nil {
			return err
		}
		cfg.AutoSync = autoSync
		if autoSync && mode == "pro" {
			cipher, err := syncclient.LoadCipher(repo.TraceDir(root), os.Getenv("TRACE_SYNC_PASSPHRASE"))
			if err != nil {
				return err
			}
			credentials.ProSyncKey = cipher.KeyForCredentialStore()
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
		if autoSync {
			if err := daemon.EnsureRunning(root); err != nil {
				return fmt.Errorf("starting background sync: %w", err)
			}
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"logged_in": true, "remote": remote, "mode": mode})
		}
		if autoSync {
			_, err = fmt.Fprintf(c.OutOrStdout(), "Automatic sync enabled for %s. New history uploads in the background.\n", remote)
		} else {
			_, err = fmt.Fprintf(c.OutOrStdout(), "Sync login saved for %s. Run trace sync when ready.\n", remote)
		}
		return err
	}}
	cmd.Flags().StringVar(&remote, "remote", "", "GitHub owner/repo")
	cmd.Flags().StringVar(&mode, "mode", "pro", "sync mode: pro or team")
	defaultEndpoint := os.Getenv("TRACE_SYNC_URL")
	if defaultEndpoint == "" {
		defaultEndpoint = defaultSyncEndpoint
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", defaultEndpoint, "Trace sync server HTTPS URL")
	cmd.Flags().StringVar(&clientID, "client-id", os.Getenv("TRACE_GITHUB_CLIENT_ID"), "GitHub client ID")
	cmd.Flags().BoolVar(&githubApp, "github-app", false, "use fine-grained GitHub App authorization")
	cmd.Flags().BoolVar(&autoSync, "auto-sync", false, "opt in to background upload after login")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the GitHub verification URL without opening a browser")
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
			client.Cipher, err = configuredCipher(client.TraceDir, cfg)
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
	cmd.AddCommand(&cobra.Command{Use: "disable-auto", Short: "Stop background sync for this repository", RunE: func(c *cobra.Command, _ []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		path := loginPath(root)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var cfg syncclient.Config
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("reading sync configuration: %w", err)
		}
		cfg.AutoSync = false
		b, err = json.Marshal(cfg)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0600); err != nil {
			return fmt.Errorf("saving sync configuration: %w", err)
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), "Automatic sync disabled. Run trace sync to upload manually.")
		return err
	}})
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
