package syncclient

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const service = "usetrace-sync"

// LoadConfigCredentials migrates only a legacy entry whose identity matches the
// checkout. Never infer ownership from a project label shared by two accounts.
func LoadConfigCredentials(cfg Config) (Credentials, error) {
	return loadConfigCredentials(cfg, LoadCredentials, SaveCredentials)
}

func loadConfigCredentials(cfg Config, read func(string) (Credentials, error), save func(string, Credentials) error) (Credentials, error) {
	if cfg.Mode != "personal" {
		return read(cfg.CredentialKey())
	}
	if cfg.ConnectionID == "" {
		return Credentials{}, errors.New("dashboard connection identity missing; run trace connect again")
	}
	credentials, err := read(cfg.CredentialKey())
	if err == nil {
		if credentials.ConnectionID != cfg.ConnectionID || (cfg.Device && credentials.Scope != "account") {
			return Credentials{}, errors.New("dashboard credential identity mismatch; run trace connect again")
		}
		return credentials, nil
	}
	if cfg.Device {
		return Credentials{}, errors.New("computer credentials unavailable; run trace login again")
	}
	credentials, err = read(cfg.legacyCredentialKey())
	if err != nil || credentials.ConnectionID != cfg.ConnectionID {
		return Credentials{}, errors.New("dashboard credentials unavailable for this connection; run trace connect again")
	}
	if err := save(cfg.CredentialKey(), credentials); err != nil {
		return Credentials{}, err
	}
	return credentials, nil
}

type Credentials struct {
	Scope            string `json:"scope,omitempty"`
	ConnectionID     string `json:"connection_id,omitempty"`
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	ExpiresAt        int64  `json:"expires_at,omitempty"`
	RefreshExpiresAt int64  `json:"refresh_expires_at,omitempty"`
	ProSyncKey       string `json:"pro_sync_key,omitempty"`
}

func (c Credentials) encode() (string, error) {
	if c.AccessToken == "" {
		return "", errors.New("missing sync credentials")
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encoding sync credentials: %w", err)
	}
	return string(b), nil
}

func decodeCredentials(value string) (Credentials, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Credentials{}, errors.New("no sync token in OS credential store")
	}
	if !strings.HasPrefix(value, "{") {
		return Credentials{AccessToken: value}, nil
	}
	var credentials Credentials
	if err := json.Unmarshal([]byte(value), &credentials); err != nil {
		return Credentials{}, fmt.Errorf("decoding sync credentials: %w", err)
	}
	if credentials.AccessToken == "" {
		return Credentials{}, errors.New("missing sync credentials")
	}
	return credentials, nil
}

func SaveToken(remote, token string) error {
	return SaveCredentials(remote, Credentials{AccessToken: token})
}

func SaveCredentials(remote string, credentials Credentials) error {
	value, err := credentials.encode()
	if remote == "" || err != nil {
		return errors.New("missing sync credentials")
	}
	cmd, err := credentialSaveCommand(runtime.GOOS, remote, value)
	if err != nil {
		return err
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("saving token in OS credential store: %w", err)
	}
	stored, err := LoadCredentials(remote)
	if err != nil || stored != credentials {
		return errors.New("OS credential store did not preserve the connection; reconnect after unlocking the credential store")
	}
	return nil
}

func credentialSaveCommand(goos, remote, value string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	switch goos {
	case "darwin":
		// A trailing -w reads getpass() from the controlling terminal, not
		// stdin. Use security's command-input mode so credentials never enter
		// process arguments and no interactive password prompt can replace them.
		if strings.ContainsAny(remote, "\r\n\x00") {
			return nil, errors.New("invalid credential account")
		}
		account := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(remote) + `"`
		input := "add-generic-password -U -a " + account + " -s " + service + " -X " + hex.EncodeToString([]byte(value)) + "\n"
		// Apple's interactive parser has a fixed 4096-byte line buffer.
		if len(input) >= 4096 {
			return nil, errors.New("credentials exceed the macOS credential transport limit")
		}
		cmd = exec.Command("security", "-i")
		cmd.Stdin = strings.NewReader(input)
	case "linux":
		cmd = exec.Command("secret-tool", "store", "--label=Trace sync", service, remote)
		cmd.Stdin = strings.NewReader(value)
	default:
		return nil, errors.New("OS credential store is not supported")
	}
	return cmd, nil
}

func LoadToken(remote string) (string, error) {
	credentials, err := LoadCredentials(remote)
	if err != nil {
		return "", err
	}
	return credentials.AccessToken, nil
}

func LoadCredentials(remote string) (Credentials, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("security", "find-generic-password", "-a", remote, "-s", service, "-w")
	case "linux":
		cmd = exec.Command("secret-tool", "lookup", service, remote)
	default:
		return Credentials{}, errors.New("OS credential store is not supported")
	}
	b, err := cmd.Output()
	if err != nil {
		return Credentials{}, fmt.Errorf("loading token from OS credential store: %w", err)
	}
	return decodeCredentials(string(b))
}
