package windsurf

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
	cases := []struct {
		name, info string
		want       event.Kind
		count      int
	}{
		{"pre_user_prompt", `"user_prompt":"add search"`, event.TurnStart, 1},
		{"post_read_code", `"file_path":"/repo/main.go"`, event.FileRead, 1},
		{"post_write_code", `"file_path":"/repo/main.go"`, event.ToolEdit, 1},
		{"post_run_command", `"command_line":"go test ./..."`, event.ToolEdit, 1},
		{"post_cascade_response", `"response":"done"`, event.TurnContext, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"agent_action_name":"` + tc.name + `","trajectory_id":"conversation-1","timestamp":"2026-01-02T03:04:05Z","tool_info":{` + tc.info + `}}`
			events, err := Parse(tc.name, []byte(body), "/repo")
			if err != nil || len(events) != tc.count {
				t.Fatalf("Parse() = %+v, %v", events, err)
			}
			if events[0].Kind != tc.want || events[0].AgentSessionID != "conversation-1" || events[0].At != 1767323045000 {
				t.Fatalf("unexpected event: %+v", events[0])
			}
			if tc.count == 2 && events[1].Kind != event.TurnEnd {
				t.Fatalf("response must end turn: %+v", events)
			}
		})
	}
	if events, err := Parse("other", []byte(`{"agent_action_name":"other"}`), "/repo"); err != nil || len(events) != 0 {
		t.Fatalf("unknown hook = %+v, %v", events, err)
	}
	if _, err := Parse("post_write_code", []byte(`{"agent_action_name":"pre_user_prompt"}`), "/repo"); err == nil {
		t.Fatal("expected event mismatch")
	}
}

func TestInstallPreservesExistingHooks(t *testing.T) {
	for _, relative := range []string{currentPath, legacyPath} {
		t.Run(relative, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, relative)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte("{\"hooks\":{\"post_write_code\":[{\"command\":\"echo keep\"}]}}\n")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := Install(root); err != nil {
					t.Fatal(err)
				}
			}
			b, err := os.ReadFile(path)
			if err != nil || strings.Count(string(b), " hook windsurf post_write_code") != 1 || !strings.Contains(string(b), "echo keep") {
				t.Fatalf("installed config = %s, %v", b, err)
			}
			if err := Uninstall(root); err != nil {
				t.Fatal(err)
			}
			b, err = os.ReadFile(path)
			if err != nil || !sameJSON(b, original) {
				t.Fatalf("uninstalled config = %s, %v", b, err)
			}
		})
	}
}

func TestUninstallPreservesLaterUserHook(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, currentPath)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), `"hooks": {`, `"hooks": {"custom":[{"command":"echo keep"}],`, 1))
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "echo keep") || strings.Contains(string(b), " hook windsurf ") {
		t.Fatalf("unexpected config after uninstall: %s, %v", b, err)
	}
}

func TestInstallKeepsActiveLegacyHooks(t *testing.T) {
	root := t.TempDir()
	for path, contents := range map[string]string{
		currentPath: `{"hooks":{}}`,
		legacyPath:  `{"hooks":{"post_write_code":[{"command":"echo legacy"}]}}`,
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, legacyPath))
	if err != nil || !strings.Contains(string(b), "echo legacy") || !strings.Contains(string(b), " hook windsurf post_write_code") {
		t.Fatalf("legacy hooks inactive: %s, %v", b, err)
	}
	b, err = os.ReadFile(filepath.Join(root, currentPath))
	if err != nil || strings.Contains(string(b), " hook windsurf ") {
		t.Fatalf("current config shadowed legacy hooks: %s, %v", b, err)
	}
}

func TestGlobalSetupPreservesBothConfigs(t *testing.T) {
	home := t.TempDir()
	original := []byte(`{"hooks":{"post_write_code":[{"command":"echo keep","show_output":false}]},"custom":true}`)
	for _, path := range globalPaths {
		p := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := InstallGlobal(home); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range globalPaths {
		b, err := os.ReadFile(filepath.Join(home, path))
		if err != nil || strings.Count(string(b), " hook windsurf-global post_write_code") != 1 || !strings.Contains(string(b), "echo keep") {
			t.Fatalf("installed %s: %s %v", path, b, err)
		}
	}
	if err := UninstallGlobal(home); err != nil {
		t.Fatal(err)
	}
	for _, path := range globalPaths {
		b, err := os.ReadFile(filepath.Join(home, path))
		if err != nil || !sameJSON(b, original) {
			t.Fatalf("removed %s: %s %v", path, b, err)
		}
	}
}

func TestGlobalSetupChecksBothFilesBeforeWriting(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, globalPaths[1])
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"hooks":{"pre_user_prompt":{}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(home); err == nil {
		t.Fatal("accepted invalid config")
	}
	if _, err := os.Stat(filepath.Join(home, globalPaths[0])); !os.IsNotExist(err) {
		t.Fatalf("other config modified: %v", err)
	}
}

func TestHasProjectHook(t *testing.T) {
	root := t.TempDir()
	if HasProjectHook(root, "pre_user_prompt") {
		t.Fatal("unexpected hook")
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if !HasProjectHook(root, "pre_user_prompt") || HasProjectHook(root, "unknown") {
		t.Fatal("project hook not detected")
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if HasProjectHook(root, "pre_user_prompt") {
		t.Fatal("removed hook still detected")
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
