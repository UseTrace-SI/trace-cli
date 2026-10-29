package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kind       event.Kind
		path       string
	}{
		{"sessionStart", `{"sessionId":"s","timestamp":1767323045000}`, event.SessionStart, ""},
		{"userPromptSubmitted", `{"sessionId":"s","prompt":"fix it"}`, event.TurnStart, ""},
		{"postToolUse", `{"sessionId":"s","toolName":"edit","toolArgs":{"path":"/repo/main.go"}}`, event.ToolEdit, "/repo/main.go"},
		{"postToolUse", `{"sessionId":"s","toolName":"view","toolArgs":{"file_path":"/repo/main.go"}}`, event.FileRead, "/repo/main.go"},
	} {
		t.Run(tc.name+tc.path, func(t *testing.T) {
			events, err := Parse(tc.name, []byte(tc.body), "/repo")
			if err != nil || len(events) != 1 || events[0].Kind != tc.kind || events[0].AgentSessionID != "s" {
				t.Fatalf("Parse() = %+v, %v", events, err)
			}
			if tc.path != "" && (len(events[0].Paths) != 1 || events[0].Paths[0] != tc.path) {
				t.Fatalf("path = %+v", events[0].Paths)
			}
		})
	}
	stop, err := Parse("agentStop", []byte(`{"sessionId":"s"}`), "/repo")
	if err != nil || len(stop) != 2 || stop[0].Kind != event.ToolEdit || stop[1].Kind != event.TurnEnd {
		t.Fatalf("stop = %+v %v", stop, err)
	}
	end, err := Parse("sessionEnd", []byte(`{"sessionId":"s","reason":"abort"}`), "/repo")
	if err != nil || len(end) != 3 || end[0].Kind != event.ToolEdit || end[1].Kind != event.TurnEnd || end[2].Kind != event.SessionEnd {
		t.Fatalf("session end = %+v %v", end, err)
	}
	if events, err := Parse("postToolUse", []byte(`{"toolName":"grep"}`), "/repo"); err != nil || len(events) != 0 {
		t.Fatalf("read-only tool = %+v, %v", events, err)
	}
	if events, err := Parse("agentStop", []byte(`{"sessionId":"s","agentId":"child"}`), "/repo"); err != nil || len(events) != 0 {
		t.Fatalf("subagent closed parent turn: %+v, %v", events, err)
	}
	if events, err := Parse("userPromptSubmitted", []byte(`{"sessionId":"s","agentId":"child","prompt":"internal"}`), "/repo"); err != nil || len(events) != 0 {
		t.Fatalf("subagent started parent turn: %+v, %v", events, err)
	}
	if events, err := Parse("postToolUse", []byte(`{"sessionId":"s","agentId":"child","toolName":"edit","toolArgs":{"path":"/repo/main.go"}}`), "/repo"); err != nil || len(events) != 1 || events[0].Kind != event.ToolEdit {
		t.Fatalf("subagent edit was not retained: %+v, %v", events, err)
	}
}

func TestInstallUninstall(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, configPath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"version\":1,\"hooks\":{\"agentStop\":[{\"type\":\"command\",\"command\":\"echo keep\"}]}}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Install(root); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil || strings.Count(string(b), " hook copilot agentStop") != 1 || !strings.Contains(string(b), "echo keep") || !strings.Contains(string(b), `"type": "command"`) {
		t.Fatalf("installed config = %s, %v", b, err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil || !sameJSON(b, original) {
		t.Fatalf("uninstalled config = %s, %v", b, err)
	}
}

func TestGlobalSetup(t *testing.T) {
	home, custom := t.TempDir(), t.TempDir()
	t.Setenv("COPILOT_HOME", custom)
	path := filepath.Join(custom, "hooks", "usetrace.json")
	if err := InstallGlobal(home); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(home); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || strings.Count(string(b), " hook copilot-global agentStop") != 1 || !strings.Contains(string(b), `"version": 1`) || !strings.Contains(string(b), `"type": "command"`) {
		t.Fatalf("installed: %s %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".copilot", "hooks", "usetrace.json")); !os.IsNotExist(err) {
		t.Fatalf("default home changed: %v", err)
	}
	if err := UninstallGlobal(home); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil || strings.Contains(string(b), " hook copilot-global ") {
		t.Fatalf("removed: %s %v", b, err)
	}
	t.Setenv("COPILOT_HOME", "relative")
	if err := InstallGlobal(home); err == nil {
		t.Fatal("accepted relative COPILOT_HOME")
	}
	if err := UninstallGlobal(home); err == nil {
		t.Fatal("removed hooks using relative COPILOT_HOME")
	}
}

func TestHasProjectHook(t *testing.T) {
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if !HasProjectHook(root, "agentStop") || HasProjectHook(root, "unknown") {
		t.Fatal("wrong project hook detection")
	}
	p := filepath.Join(root, configPath)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	hooks := cfg["hooks"].(map[string]any)
	hook := hooks["agentStop"].([]any)[0].(map[string]any)
	hook["osx"] = "echo custom"
	b, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	if HasProjectHook(root, "agentStop") || !HasProjectHook(root, "sessionStart") {
		t.Fatal("modified project hook suppressed global hook")
	}
}

func TestToolArgsAndCWD(t *testing.T) {
	for _, input := range []string{`{"path":"file.go"}`, `"{\"path\":\"file.go\"}"`} {
		body := `{"sessionId":"s","cwd":"/repo/sub","toolName":"edit","toolArgs":` + input + `}`
		got, err := Parse("postToolUse", []byte(body), "/repo")
		if err != nil || len(got) != 1 || len(got[0].Paths) != 1 || got[0].Paths[0] != "/repo/sub/file.go" {
			t.Fatalf("Parse(%s): %+v %v", input, got, err)
		}
	}
}

func TestUninstallRemovesLegacyTraceHooksOnly(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, legacyPath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := `{"hooks":{"agentStop":[{"type":"command","command":"trace hook copilot agentStop"},{"type":"command","command":"echo keep"}]},"other":true}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(b), "trace hook copilot") || !strings.Contains(string(b), "echo keep") || !strings.Contains(string(b), `"other": true`) {
		t.Fatalf("legacy cleanup: %s %v", b, err)
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
