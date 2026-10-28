package syncclient

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const service = "usetrace-sync"

func SaveToken(remote, token string) error {
	if remote == "" || token == "" {
		return errors.New("missing sync credentials")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("security", "add-generic-password", "-U", "-a", remote, "-s", service, "-w", token)
	case "linux":
		cmd = exec.Command("secret-tool", "store", "--label=Trace sync", service, remote)
		cmd.Stdin = strings.NewReader(token)
	default:
		return errors.New("OS credential store is not supported")
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("saving token in OS credential store: %w", err)
	}
	return nil
}

func LoadToken(remote string) (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("security", "find-generic-password", "-a", remote, "-s", service, "-w")
	case "linux":
		cmd = exec.Command("secret-tool", "lookup", service, remote)
	default:
		return "", errors.New("OS credential store is not supported")
	}
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("loading token from OS credential store: %w", err)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", errors.New("no sync token in OS credential store")
	}
	return token, nil
}
