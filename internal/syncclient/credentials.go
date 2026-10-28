package syncclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const service = "usetrace-sync"

type Credentials struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	ExpiresAt        int64  `json:"expires_at,omitempty"`
	RefreshExpiresAt int64  `json:"refresh_expires_at,omitempty"`
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
	return nil
}

func credentialSaveCommand(goos, remote, value string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	switch goos {
	case "darwin":
		cmd = exec.Command("security", "add-generic-password", "-U", "-a", remote, "-s", service, "-w")
		cmd.Stdin = strings.NewReader(value + "\n" + value + "\n")
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
