package kimi

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestInstallPreservesConfig(t *testing.T) {
	t.Setenv("KIMI_CODE_HOME", "")
	home := t.TempDir()
	p, err := configPath(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	original := "model = \"example\"\n\n[[hooks]]\nevent = \"Stop\"\ncommand = \"user-hook\"\n"
	if err := os.WriteFile(p, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(home); err != nil {
		t.Fatal(err)
	}
	if err := Install(home); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || strings.Count(string(b), start) != 1 || !strings.Contains(string(b), "user-hook") {
		t.Fatalf("config: %s %v", b, err)
	}
	if err := Uninstall(home); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(p)
	if err != nil || string(b) != original {
		t.Fatalf("restored: %s %v", b, err)
	}
}

func TestCustomHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", dir)
	if err := Install(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestRejectRelativeCustomHome(t *testing.T) {
	for _, custom := range []string{"relative-profile", "~/profile", "."} {
		t.Run(custom, func(t *testing.T) {
			t.Setenv("KIMI_CODE_HOME", custom)
			if err := Install(t.TempDir()); err == nil || !strings.Contains(err.Error(), "must be absolute") {
				t.Fatalf("Install error = %v", err)
			}
			if err := Uninstall(t.TempDir()); err == nil || !strings.Contains(err.Error(), "must be absolute") {
				t.Fatalf("Uninstall error = %v", err)
			}
		})
	}
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kind       event.Kind
	}{
		{"SessionStart", `{"session_id":"s"}`, event.SessionStart},
		{"UserPromptSubmit", `{"session_id":"s","prompt":"hello"}`, event.TurnStart},
		{"PostToolUse", `{"session_id":"s","tool_name":"WriteFile","tool_input":{"file_path":"a.go"}}`, event.ToolEdit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.name, []byte(tc.body), "/repo")
			if err != nil || len(got) != 1 || got[0].Agent != "kimi" || got[0].Kind != tc.kind || got[0].At == 0 {
				t.Fatalf("events: %+v, %v", got, err)
			}
		})
	}
}

func TestStructuredPrompt(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	body := `{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":[{"type":"text","text":"Explain main.txt"},{"type":"image","text":"ignore"},{"type":"text","text":"` + secret + `"}]}`
	got, err := Parse("UserPromptSubmit", []byte(body), "/repo")
	if err != nil || len(got) != 1 || got[0].Kind != event.TurnStart || !strings.Contains(got[0].Prompt, "Explain main.txt") || strings.Contains(got[0].Prompt, secret) || strings.Contains(got[0].Prompt, "ignore") {
		t.Fatalf("prompt: %+v, %v", got, err)
	}
	got, err = Parse("UserPromptSubmit", []byte(`{"session_id":"s","agent_id":"subagent","prompt":[{"type":"text","text":"child"}]}`), "/repo")
	if err != nil || len(got) != 0 {
		t.Fatalf("subagent prompt: %+v, %v", got, err)
	}
}

func TestFinalEventsAndResponse(t *testing.T) {
	for _, name := range []string{"Stop", "StopFailure", "Interrupt", "SessionEnd"} {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(name, []byte(`{"session_id":"s"}`), "/repo")
			count := 2
			if name == "SessionEnd" {
				count = 3
			}
			if err != nil || len(got) != count || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnEnd {
				t.Fatalf("events: %+v, %v", got, err)
			}
			if name == "SessionEnd" && got[2].Kind != event.SessionEnd {
				t.Fatalf("events: %+v", got)
			}
		})
	}
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	got, err := Parse("Stop", []byte(`{"session_id":"s","last_assistant_message":"done `+secret+`"}`), "/repo")
	if err != nil || len(got) != 3 || got[1].Kind != event.TurnContext || strings.Contains(got[1].Text, secret) || !strings.Contains(got[1].Text, "redacted") || got[2].Kind != event.TurnEnd {
		t.Fatalf("response: %+v, %v", got, err)
	}
	for _, body := range []string{`{}`, `{"session_id":"s","hook_event_name":"Stop"}`} {
		got, err := Parse("PostToolUse", []byte(body), "/repo")
		if err != nil || len(got) != 0 {
			t.Fatalf("unexpected events: %+v, %v", got, err)
		}
	}
}

func TestSubagentLifecycleDoesNotCloseParent(t *testing.T) {
	for _, name := range []string{"SessionStart", "UserPromptSubmit", "Stop", "StopFailure", "Interrupt", "SessionEnd"} {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(name, []byte(`{"session_id":"s","agent_id":"child","prompt":"child"}`), "/repo")
			if err != nil || len(got) != 0 {
				t.Fatalf("subagent event: %+v, %v", got, err)
			}
		})
	}
	got, err := Parse("PostToolUse", []byte(`{"session_id":"s","agent_id":"child","tool_name":"WriteFile","tool_input":{"file_path":"a.go"}}`), "/repo")
	if err != nil || len(got) != 1 || got[0].Kind != event.ToolEdit {
		t.Fatalf("subagent tool edit: %+v, %v", got, err)
	}
}

func TestToolPathUsesSessionDirectory(t *testing.T) {
	for _, tc := range []struct {
		name, body, path string
	}{
		{"nested", `{"session_id":"s","cwd":"/repo/sub","tool_name":"WriteFile","tool_input":{"file_path":"a.go"}}`, "/repo/sub/a.go"},
		{"absolute", `{"session_id":"s","cwd":"/repo/sub","tool_name":"ReadFile","tool_input":{"file_path":"/repo/other.go"}}`, "/repo/other.go"},
		{"no cwd", `{"session_id":"s","tool_name":"WriteFile","tool_input":{"file_path":"a.go"}}`, "a.go"},
		{"native write", `{"session_id":"s","cwd":"/repo/sub","tool_name":"Write","tool_input":{"path":"a.go"}}`, "/repo/sub/a.go"},
		{"native edit", `{"session_id":"s","cwd":"/repo/sub","tool_name":"Edit","tool_input":{"path":"a.go"}}`, "/repo/sub/a.go"},
		{"native read", `{"session_id":"s","cwd":"/repo/sub","tool_name":"Read","tool_input":{"path":"a.go"}}`, "/repo/sub/a.go"},
		{"media read", `{"session_id":"s","cwd":"/repo/sub","tool_name":"ReadMediaFile","tool_input":{"path":"image.png"}}`, "/repo/sub/image.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse("PostToolUse", []byte(tc.body), "/repo")
			if err != nil || len(got) != 1 || len(got[0].Paths) != 1 || got[0].Paths[0] != tc.path {
				t.Fatalf("tool event = %+v, %v", got, err)
			}
		})
	}
}

func TestNativeReadKind(t *testing.T) {
	for _, tool := range []string{"Read", "ReadMediaFile"} {
		got, err := Parse("PostToolUse", []byte(`{"session_id":"s","tool_name":"`+tool+`","tool_input":{"path":"a.go"}}`), "/repo")
		if err != nil || len(got) != 1 || got[0].Kind != event.FileRead {
			t.Fatalf("%s: %+v, %v", tool, got, err)
		}
	}
}

func TestEditedManagedBlockIsPreserved(t *testing.T) {
	t.Setenv("KIMI_CODE_HOME", "")
	home := t.TempDir()
	if err := Install(home); err != nil {
		t.Fatal(err)
	}
	p, err := configPath(home)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	modified := strings.Replace(string(b), "event = \"Stop\"", "event = \"Stop\"\ntimeout = 5", 1)
	if err := os.WriteFile(p, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	if Install(home) == nil || Uninstall(home) == nil {
		t.Fatal("accepted edited managed block")
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != modified {
		t.Fatalf("modified config changed: %v", err)
	}
}

func TestRejectDamagedManagedBlock(t *testing.T) {
	if _, err := rewrite("# BEGIN TRACE KIMI HOOKS\nuser settings\n", true); err == nil {
		t.Fatal("accepted incomplete managed block")
	}
}

func TestKimiDoctorAcceptsConfig(t *testing.T) {
	binary, err := exec.LookPath("kimi")
	if err != nil {
		t.Skip("Kimi Code CLI not installed")
	}
	dir := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", dir)
	if err := Install(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "doctor", "config", filepath.Join(dir, "config.toml"))
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Kimi rejected generated config: %v: %s", err, out)
	}
}
