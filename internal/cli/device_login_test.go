package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
	"github.com/UseTrace-SI/trace-cli/internal/userstate"
	"github.com/spf13/cobra"
)

func TestComputerLoginRoutesProjectsAndPreservesProjectPause(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRACE_HOME", "")
	first, second := competitiveRepo(t), competitiveRepo(t)
	if output, err := exec.Command("git", "-C", first, "remote", "add", "origin", "https://github.com/owner/first.git").CombinedOutput(); err != nil {
		t.Fatalf("remote: %s %v", output, err)
	}
	cfg := syncclient.Config{Device: true, Mode: "personal", Endpoint: "https://trace.example", Remote: "device/all-projects", ConnectionID: "computer-1", AutoSync: true}
	path, _ := userstate.Path("device.json")
	if err := writeSyncConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	a, err := readSyncConfig(first)
	if err != nil || a.Remote != "owner/first" || !a.AutoSync {
		t.Fatalf("first project: %+v %v", a, err)
	}
	b, err := readSyncConfig(second)
	if err != nil || !strings.HasPrefix(b.Remote, "local/") || b.Remote == a.Remote || a.CredentialKey() != b.CredentialKey() {
		t.Fatalf("second project: %+v %v", b, err)
	}
	jsonOutput := false
	cmd := syncCommand(&first, &jsonOutput)
	cmd.SetArgs([]string{"disable-auto"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// A later login updates credentials while retaining a project's pause.
	cfg.ConnectionID = "computer-2"
	if err := writeSyncConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	a, err = readSyncConfig(first)
	if err != nil || a.AutoSync || a.ConnectionID != "computer-2" {
		t.Fatalf("project pause or renewed credential lost: %+v %v", a, err)
	}
	b, _ = readSyncConfig(second)
	if !b.AutoSync || b.ConnectionID != "computer-2" {
		t.Fatalf("global default did not renew: %+v", b)
	}
	cmd = syncCommand(&first, &jsonOutput)
	cmd.SetArgs([]string{"enable-auto"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	a, _ = readSyncConfig(first)
	if !a.AutoSync {
		t.Fatal("project background sync did not resume")
	}
	// An explicit project-only account is never replaced by global login.
	a.Device, a.ConnectionID = false, "project-only"
	if err := writeSyncConfig(loginPath(first), a); err != nil {
		t.Fatal(err)
	}
	a, _ = readSyncConfig(first)
	if a.ConnectionID != "project-only" || a.Device {
		t.Fatal("explicit project connection replaced")
	}
}

func TestEnableAllIntegrationsAndDisableKeepLogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRACE_HOME", "")
	for _, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "GEMINI_CLI_HOME", "HERMES_HOME", "OPENCLAUDE_CONFIG_DIR", "OH_PERSISTENCE_DIR", "COPILOT_HOME", "CLINE_DIR", "PI_PROFILE", "PI_CONFIG_DIR", "PI_CODING_AGENT_DIR", "OPENCODE_CONFIG_DIR", "KIMI_CODE_HOME"} {
		t.Setenv(name, "")
	}
	path, _ := userstate.Path("device.json")
	if err := writeSyncConfig(path, syncclient.Config{ConnectionID: "retained"}); err != nil {
		t.Fatal(err)
	}
	jsonOutput := true
	for i := 0; i < 2; i++ {
		cmd := enableCommand(&jsonOutput)
		var selected []string
		for _, item := range globalIntegrations {
			selected = append(selected, item.Name)
		}
		cmd.SetArgs([]string{"--agent", strings.Join(selected, ",")})
		cmd.SetOut(&bytes.Buffer{})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if userstate.Disabled() {
			t.Fatal("enable left recording paused")
		}
		cmd = disableCommand(&jsonOutput)
		cmd.SetOut(&bytes.Buffer{})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !userstate.Disabled() {
			t.Fatal("disable did not pause recording")
		}
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.Contains(b, []byte("retained")) {
		t.Fatal("disable erased the account")
	}
}

func TestDisableCancelsAutomaticUpload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRACE_HOME", "")
	root := competitiveRepo(t)
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err := e.DB.Exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex','repo',1)"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	client := &syncclient.Client{Config: syncclient.Config{Endpoint: "https://trace.example", Remote: "a/b", Mode: "personal"}, Token: "test", TraceDir: filepath.Join(root, ".trace"), DB: e.DB.DB, Blobs: e.Blobs,
		HTTP: &http.Client{Transport: loginRoundTrip(func(r *http.Request) (*http.Response, error) {
			close(started)
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := syncAutomatically(ctx, client); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("upload did not start")
	}
	if err := userstate.SetDisabled(true); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("upload was not cancelled: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("paused upload continued")
	}
}

func TestPerAgentPauseAndGlobalResumeSelection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRACE_HOME", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	jsonOutput := true
	run := func(cmd *cobra.Command, args ...string) {
		t.Helper()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	run(enableCommand(&jsonOutput), "--agent", "codex,claude-code")
	run(disableCommand(&jsonOutput), "--agent", "claude-code")
	if userstate.DisabledFor("codex") || !userstate.DisabledFor("claude-code") {
		t.Fatal("per-agent pause affected the wrong selection")
	}
	run(disableCommand(&jsonOutput))
	run(enableCommand(&jsonOutput))
	if userstate.DisabledFor("codex") || !userstate.DisabledFor("claude-code") {
		t.Fatal("global resume lost the saved per-agent pause")
	}
	run(enableCommand(&jsonOutput), "claude-code")
	if userstate.DisabledFor("claude-code") {
		t.Fatal("selected agent did not resume")
	}
	root := competitiveRepo(t)
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ev := event.Event{Agent: "codex", AgentSessionID: "paused-agent", Worktree: root, At: 1}
	for _, kind := range []event.Kind{event.SessionStart, event.TurnStart} {
		ev.Kind = kind
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
	}
	if !watchedCaptureAllowed(e) {
		t.Fatal("enabled agent file changes blocked")
	}
	run(disableCommand(&jsonOutput), "codex")
	if watchedCaptureAllowed(e) {
		t.Fatal("file watcher ignored the active agent's pause")
	}
}

func TestInstalledAgentDiscoveryAndMachineReadableControls(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRACE_HOME", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".cursor"), 0700); err != nil {
		t.Fatal(err)
	}
	jsonOutput := true
	cmd := enableCommand(&jsonOutput)
	cmd.SetArgs([]string{"--all"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Configured bool     `json:"configured"`
		Selected   []string `json:"selected_agents"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Configured || !slices.Contains(result.Selected, "codex") || !slices.Contains(result.Selected, "cursor") {
		t.Fatalf("installed agents not selected: %s", output.String())
	}
	output.Reset()
	cmd = agentsCommand(&jsonOutput)
	cmd.SetOut(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Agents []struct {
			Name       string `json:"name"`
			Configured bool   `json:"configured"`
			Enable     string `json:"enable_command"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(output.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	for _, item := range listing.Agents {
		if item.Name == "codex" && (!item.Configured || item.Enable != "trace enable --agent codex") {
			t.Fatalf("invalid discovery result: %+v", item)
		}
	}
}

func TestLoginStatusNeedsNoProjectOrBrowser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRACE_HOME", "")
	root := ""
	jsonOutput := true
	cmd := loginCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"--status"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["configured"] != false {
		t.Fatalf("unexpected login status: %s", output.String())
	}
}

func TestProjectRemoteIdentityAcrossComputersAndGitProviders(t *testing.T) {
	ssh := projectRemoteName("git@gitlab.com:team/subgroup/repo.git")
	https := projectRemoteName("https://gitlab.com/team/subgroup/repo.git")
	if ssh == "" || ssh != https || !remoteName.MatchString(ssh) {
		t.Fatalf("unstable project identity: %s %s", ssh, https)
	}
	if ssh == projectRemoteName("https://other.example/team/subgroup/repo.git") {
		t.Fatal("different Git services share project identity")
	}
	if got := projectRemoteName("https://private:token@gitlab.com/team/subgroup/repo.git"); got != ssh || strings.Contains(got, "token") {
		t.Fatalf("credentials affected identity: %s", got)
	}
	if got := projectRemoteName("git@github.com:Owner/Repo.git"); got != "owner/repo" {
		t.Fatalf("GitHub labels changed: %s", got)
	}
}
