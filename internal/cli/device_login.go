package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
	"github.com/UseTrace-SI/trace-cli/internal/userstate"
	"github.com/spf13/cobra"
)

func runDeviceLogin(c *cobra.Command, endpoint, device string, noBrowser, jsonOutput bool) error {
	path, err := userstate.Path("device.json")
	if err != nil {
		return err
	}
	_, statErr := os.Stat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if device == "" {
		device, _ = os.Hostname()
	}
	if device == "" {
		device = "My computer"
	}
	cfg := syncclient.Config{Endpoint: endpoint, Remote: "device/all-projects", Mode: "personal", Device: true, AutoSync: true}
	ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	flow, err := syncclient.StartDeviceLogin(ctx, nil, cfg, device)
	if err != nil {
		return err
	}
	if jsonOutput {
		if err := json.NewEncoder(c.ErrOrStderr()).Encode(map[string]any{"status": "authorization_pending", "user_code": flow.UserCode, "verification_uri": flow.VerificationURI, "expires_at": flow.ExpiresAt, "scope": "account"}); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(c.ErrOrStderr(), "Your one-time code: %s\nOpen %s and enter this code to approve this computer.\nApproval connects all traced projects to your private dashboard. When enabled, existing history and future turns upload automatically. Source bytes may contain credentials and are server-encrypted.\n", flow.UserCode, flow.VerificationURI); err != nil {
			return err
		}
	}
	if !noBrowser {
		_ = openLoginBrowser(flow.VerificationURI)
	}
	credentials, err := syncclient.WaitPairing(ctx, nil, endpoint, flow)
	if err != nil {
		return err
	}
	cfg.ConnectionID, cfg.ConnectionExpiresAt = credentials.ConnectionID, credentials.ExpiresAt
	if err := syncclient.SaveCredentials(cfg.CredentialKey(), credentials); err != nil {
		return err
	}
	// First login is separate from enabling recording/uploads. Failed login
	// attempts do not change an existing user's recording preferences.
	if os.IsNotExist(statErr) {
		if err := userstate.SetDisabled(true); err != nil {
			return err
		}
	}
	if err := writeSyncConfig(path, cfg); err != nil {
		return fmt.Errorf("saving computer connection: %w", err)
	}
	if jsonOutput {
		return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"logged_in": true, "scope": "account", "dashboard": strings.TrimRight(endpoint, "/") + "/workspace", "enabled": !userstate.Disabled()})
	}
	_, err = fmt.Fprintf(c.OutOrStdout(), "Logged in. Run trace enable to record projects and sync automatically.\nDashboard: %s/workspace\n", strings.TrimRight(endpoint, "/"))
	return err
}

func deviceLoginStatus(c *cobra.Command, jsonOutput bool) error {
	path, err := userstate.Path("device.json")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	var cfg syncclient.Config
	configured := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if configured {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
		if !cfg.Device || cfg.ConnectionID == "" {
			return fmt.Errorf("invalid computer login; run trace login")
		}
	}
	expired := configured && cfg.ConnectionExpiresAt > 0 && cfg.ConnectionExpiresAt <= time.Now().UnixMilli()
	if jsonOutput {
		return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"configured": configured, "expired": expired, "paused": userstate.Disabled(), "endpoint": cfg.Endpoint, "expires_at": cfg.ConnectionExpiresAt, "scope": "account"})
	}
	if !configured {
		_, err = fmt.Fprintln(c.OutOrStdout(), "No computer login saved. Run trace login.")
		return err
	}
	_, err = fmt.Fprintf(c.OutOrStdout(), "Computer login: %s\nExpired: %t\nTrace paused: %t\n", cfg.Endpoint, expired, userstate.Disabled())
	return err
}

func writeSyncConfig(path string, cfg syncclient.Config) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".connection-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

// Explicit project settings always take precedence, including a paused project.
// Account credentials remain in the OS credential store; neither config stores
// the token. Dynamic defaults let existing daemons discover a later login.
func readSyncConfig(root string) (syncclient.Config, error) {
	var cfg syncclient.Config
	var projectAuto *bool
	data, err := os.ReadFile(loginPath(root))
	if err == nil {
		err = json.Unmarshal(data, &cfg)
		if err != nil || !cfg.Device {
			return cfg, err
		}
		auto := cfg.AutoSync
		projectAuto = &auto
	}
	if err != nil && !os.IsNotExist(err) {
		return cfg, err
	}
	path, err := userstate.Path("device.json")
	if err != nil {
		return cfg, err
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if !cfg.Device || cfg.Mode != "personal" || cfg.ConnectionID == "" {
		return cfg, fmt.Errorf("invalid computer connection; run trace login again")
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	if projectAuto != nil {
		cfg.AutoSync = *projectAuto
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, remoteErr := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "origin").Output()
	if remoteErr == nil {
		cfg.Remote = projectRemoteName(strings.TrimSpace(string(output)))
	}
	if remoteErr != nil || cfg.Remote == "" {
		cfg.Remote = localProjectName(root)
	}
	return cfg, nil
}

func localProjectName(root string) string {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		canonical = filepath.Clean(root)
	}
	return projectName("local", filepath.Base(canonical), canonical)
}

func projectRemoteName(raw string) string {
	if name, err := githubRemote(raw); err == nil {
		return strings.ToLower(name)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// SCP-style Git remotes use user@host:path. Credentials/user names are
		// deliberately omitted from the project identity.
		_, tail, ok := strings.Cut(raw, "@")
		if !ok {
			return ""
		}
		host, path, ok := strings.Cut(tail, ":")
		if !ok || host == "" || path == "" {
			return ""
		}
		u, err = url.Parse("ssh://" + host + "/" + path)
	}
	if err != nil || u.Host == "" || u.Path == "" {
		return ""
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if path == "" {
		return ""
	}
	return projectName("project", filepath.Base(path), strings.ToLower(u.Host)+"/"+path)
}

func projectName(namespace, base, identity string) string {
	slug := strings.Map(func(r rune) rune {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			return unicode.ToLower(r)
		}
		return '-'
	}, base)
	if len(slug) > 60 {
		slug = slug[:60]
	}
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("%s/%s-%x", namespace, slug, digest[:6])
}
