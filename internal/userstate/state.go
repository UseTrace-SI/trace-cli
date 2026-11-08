// Package userstate stores per-user recording controls, shared by all projects.
package userstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func Path(name string) (string, error) {
	if directory := os.Getenv("TRACE_HOME"); directory != "" {
		if !filepath.IsAbs(directory) {
			return "", fmt.Errorf("TRACE_HOME must be an absolute directory")
		}
		return filepath.Join(directory, name), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".trace", name), nil
}

// A missing marker preserves existing installations; other failures pause
// capture rather than ignoring a recording control that cannot be read.
func Disabled() bool {
	path, err := Path("disabled")
	if err != nil {
		return true
	}
	_, err = os.Stat(path)
	return !os.IsNotExist(err)
}

func SetDisabled(disabled bool) error {
	path, err := Path("disabled")
	if err != nil {
		return err
	}
	if !disabled {
		err = os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("disabled\n"), 0600)
}

func agentPath(agent string) (string, error) {
	if agent == "" || strings.Trim(agent, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return "", fmt.Errorf("invalid agent name")
	}
	return Path(filepath.Join("agents", agent+".disabled"))
}

func AgentDisabled(agent string) bool {
	path, err := agentPath(agent)
	if err != nil {
		return true
	}
	_, err = os.Stat(path)
	return !os.IsNotExist(err)
}

func DisabledFor(agent string) bool { return Disabled() || AgentDisabled(agent) }

func SetAgentDisabled(agent string, disabled bool) error {
	path, err := agentPath(agent)
	if err != nil {
		return err
	}
	if !disabled {
		err = os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("disabled\n"), 0600)
}

func ConfiguredAgents() ([]string, error) {
	path, err := Path("integrations.json")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var agents []string
	if err = json.Unmarshal(data, &agents); err != nil {
		return nil, err
	}
	for _, agent := range agents {
		if _, err := agentPath(agent); err != nil {
			return nil, err
		}
	}
	return agents, nil
}

func RememberAgents(selected []string) error {
	agents, err := ConfiguredAgents()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, agent := range append(agents, selected...) {
		if _, err := agentPath(agent); err != nil {
			return err
		}
		seen[agent] = true
	}
	agents = nil
	for agent := range seen {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	path, err := Path("integrations.json")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(agents)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".integrations-*")
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
