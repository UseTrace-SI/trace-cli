package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reviewFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	git("init", "-q")
	for _, path := range []string{"source.go", ".env", "literal[1].go"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("old\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-f", ".")
	for path, content := range map[string]string{"source.go": "new sk-abcdefghijklmnopqrstuvwxyz1234567890\n", ".env": "UNKNOWN_SECRET=never-send-this\n", "literal[1].go": "literal change\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestReviewPlansRedactSecretsExcludeSecretPathsAndUseLiteralPaths(t *testing.T) {
	root := reviewFixture(t)
	jsonOutput := true
	cmd := reviewCommand(&root, &jsonOutput)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--agents", "claude-code,codex", "--no-launch"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var report reviewReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 2 {
		t.Fatalf("not independent reviewers: %+v", report)
	}
	for _, r := range report.Results {
		if r.Status != "planned" || !strings.Contains(r.Prompt, "source.go") || !strings.Contains(r.Prompt, "literal change") || strings.Contains(r.Prompt, "UNKNOWN_SECRET") || strings.Contains(r.Prompt, "sk-abcdefghijklmnopqrstuvwxyz") || !strings.Contains(r.Prompt, "redacted") {
			t.Fatalf("unsafe review plan: %+v", r)
		}
	}
}

func TestReviewRunsBothProvidersAndReportsFailureWithoutDiscardingSuccess(t *testing.T) {
	root := reviewFixture(t)
	bin := t.TempDir()
	for name, script := range map[string]string{"claude": "#!/bin/sh\ncat >/dev/null\nprintf 'successful review\\n'\n", "codex": "#!/bin/sh\ncat >/dev/null\nprintf 'partial review\\n'\nexit 3\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	jsonOutput := true
	cmd := reviewCommand(&root, &jsonOutput)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--agents", "claude-code,codex"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("failed reviewer accepted")
	}
	var report reviewReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("invalid report: %s %v", &out, err)
	}
	if len(report.Results) != 2 || report.Results[0].Status != "completed" || report.Results[0].Output != "successful review\n" || report.Results[1].Status != "failed" {
		t.Fatalf("lost independent result: %+v", report)
	}
}

func TestReviewRejectsInvalidRequestsAndGitErrors(t *testing.T) {
	root := reviewFixture(t)
	jsonOutput := false
	for _, args := range [][]string{{"--agents", "codex,codex"}, {"--agents", "codex", "--agent", "claude-code"}, {"--agent", "unknown"}, {"--timeout", "0s"}} {
		cmd := reviewCommand(&root, &jsonOutput)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	root = t.TempDir()
	cmd := reviewCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"--no-launch"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("ignored git error")
	}
}

func TestReviewTimeoutKillsTheOwnedProcessGroup(t *testing.T) {
	root := reviewFixture(t)
	bin := t.TempDir()
	marker := filepath.Join(bin, "survived")
	script := "#!/bin/sh\ncat >/dev/null\n(sleep 2; touch '" + marker + "') &\nwait\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	jsonOutput := true
	cmd := reviewCommand(&root, &jsonOutput)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--timeout", "1s"})
	started := time.Now()
	if err := cmd.Execute(); err == nil {
		t.Fatal("timed-out reviewer succeeded")
	}
	var report reviewReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Results) != 1 || report.Results[0].Status != "timed_out" {
		t.Fatalf("timeout status: %s %v", &out, err)
	}
	if time.Since(started) > 2500*time.Millisecond {
		t.Fatal("review deadline did not stop its child processes")
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a review subprocess survived cancellation")
	}
}
