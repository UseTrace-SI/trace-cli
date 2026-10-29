package poolside

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestSetupPreservesExistingSettings(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".config", "poolside", "settings.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	original := "pool:\n  default_agent_server: custom\n"
	if err := os.WriteFile(p, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Setup(home, false); err != nil {
		t.Fatal(err)
	}
	if err := Setup(home, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || strings.Count(string(b), "hook poolside ") != 4 {
		t.Fatalf("managed hooks: %s, %v", b, err)
	}
	modified := append(append([]byte(nil), b...), []byte("web_search:\n  provider: custom\n")...)
	if err := os.WriteFile(p, modified, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Setup(home, true); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != original+"web_search:\n  provider: custom\n" {
		t.Fatalf("user edits changed: %s, %v", got, err)
	}
}

func TestSetupRewritesMovedExecutable(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "reinstall", true: "remove"}[remove], func(t *testing.T) {
			home := t.TempDir()
			p := filepath.Join(home, ".config", "poolside", "settings.yaml")
			if err := Setup(home, false); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(b), "\n")
			idx := 0
			for i, line := range lines {
				if strings.HasPrefix(line, "      command: ") {
					encoded, _ := json.Marshal("/old/trace hook poolside " + names[idx])
					lines[i] = "      command: " + string(encoded)
					idx++
				}
			}
			if idx != len(names) {
				t.Fatalf("missing commands: %d", idx)
			}
			old := strings.Join(lines, "\n")
			if err := os.WriteFile(p, []byte(old), 0600); err != nil {
				t.Fatal(err)
			}
			if err := Setup(home, remove); err != nil {
				t.Fatal(err)
			}
			if remove {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Fatalf("file remains: %v", err)
				}
			} else {
				updated, err := os.ReadFile(p)
				if err != nil || string(updated) != string(b) {
					t.Fatalf("hook paths not updated: %s, %v", updated, err)
				}
			}
		})
	}
}

func TestSetupRejectsAmbiguousSettings(t *testing.T) {
	for _, original := range []string{"hooks:\n  Stop: []\n", "'hooks': {}\n", "---\npool: {}\n", "<<: *defaults\n"} {
		t.Run(original, func(t *testing.T) {
			home := t.TempDir()
			p := filepath.Join(home, ".config", "poolside", "settings.yaml")
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if err := Setup(home, false); err == nil {
				t.Fatal("accepted ambiguous settings")
			}
			b, err := os.ReadFile(p)
			if err != nil || string(b) != original {
				t.Fatalf("user settings changed: %s, %v", b, err)
			}
		})
	}
}

func TestSetupRejectsEditedManagedBlock(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".config", "poolside", "settings.yaml")
	if err := Setup(home, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	modified := strings.Replace(string(b), "matcher:", "custom:", 1)
	if err := os.WriteFile(p, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Setup(home, true); err == nil {
		t.Fatal("removed edited managed block")
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != modified {
		t.Fatalf("settings changed: %s, %v", got, err)
	}
}

func TestSetupMergesExistingHookLists(t *testing.T) {
	for _, original := range []string{
		"hooks:\n  Stop:\n    - name: keep-stop\n      matcher: \"*\"\n      command: echo stop\n  PostToolUse:\n    - name: keep-tool\n      matcher: edit\n      command: echo edit\nother: value\n",
		"pool: {}\nhooks: # existing\n  PreToolUse:\n    - name: keep\n      matcher: shell\n      command: echo keep\n",
	} {
		t.Run(original, func(t *testing.T) {
			home := t.TempDir()
			p := filepath.Join(home, ".config", "poolside", "settings.yaml")
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if err := Setup(home, false); err != nil {
				t.Fatal(err)
			}
			first, err := os.ReadFile(p)
			if err != nil || strings.Count(string(first), "hook poolside ") != 4 {
				t.Fatalf("merged: %s %v", first, err)
			}
			if err := Setup(home, false); err != nil {
				t.Fatal(err)
			}
			again, err := os.ReadFile(p)
			if err != nil || string(first) != string(again) {
				t.Fatalf("not idempotent: %s %v", again, err)
			}
			if err := Setup(home, true); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(p)
			if err != nil || string(got) != original {
				t.Fatalf("settings changed: %q %v", got, err)
			}
		})
	}
}

func TestRemovingNewEventPreservesLaterUserHook(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".config", "poolside", "settings.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("hooks:\n  PreToolUse:\n    - name: keep\n      command: echo keep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Setup(home, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	modified := strings.Replace(string(b), "  # END TRACE POOLSIDE Stop\n", "  # END TRACE POOLSIDE Stop\n    - name: later\n      command: echo later\n", 1)
	if err := os.WriteFile(p, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Setup(home, true); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(got), "  Stop:\n    - name: later\n      command: echo later\n") || strings.Contains(string(got), "TRACE POOLSIDE") {
		t.Fatalf("lost user hook: %s %v", got, err)
	}
}

func TestSetupRejectsUnsupportedExistingHookLists(t *testing.T) {
	for _, original := range []string{
		"hooks:\n  Stop:\n      - name: indented\n        command: echo hi\n",
		"hooks:\n  Stop:\n    - name: \"trace-stop\"\n      command: echo custom\n",
		"hooks:\n  Stop: &list\n    - name: keep\n      command: echo hi\n",
	} {
		home := t.TempDir()
		p := filepath.Join(home, ".config", "poolside", "settings.yaml")
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Setup(home, false); err == nil {
			t.Fatalf("accepted %q", original)
		}
		got, err := os.ReadFile(p)
		if err != nil || string(got) != original {
			t.Fatalf("settings changed: %s %v", got, err)
		}
	}
}

func TestParse(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		kind       event.Kind
		count      int
	}{
		{"SessionStart", `{"session_id":"s"}`, event.SessionStart, 1},
		{"UserPromptSubmit", `{"session_id":"s","prompt":"fix ghp_abcdefghijklmnopqrstuvwxyz"}`, event.TurnStart, 1},
		{"PostToolUse", `{"session_id":"s","tool_name":"write","tool_input":{"path":"a.go"}}`, event.ToolEdit, 1},
		{"PostToolUse", `{"session_id":"s","tool_name":"read","tool_input":{"path":"a.go"}}`, event.FileRead, 1},
		{"Stop", `{"session_id":"s","reason":"end_turn"}`, event.ToolEdit, 2},
		{"Stop", `{"session_id":"s","hook_event_name":"SessionStart"}`, "", 0},
		{"Stop", `{}`, "", 0},
	} {
		t.Run(tt.name+tt.body, func(t *testing.T) {
			got, err := Parse(tt.name, []byte(tt.body), "/repo")
			if err != nil || len(got) != tt.count {
				t.Fatalf("events: %#v, %v", got, err)
			}
			if len(got) > 0 && (got[0].Kind != tt.kind || strings.Contains(got[0].Prompt, "ghp_abcdefghijklmnopqrstuvwxyz")) {
				t.Fatalf("event: %#v", got[0])
			}
			if tt.name == "Stop" && len(got) == 2 && got[1].Kind != event.TurnEnd {
				t.Fatalf("stop did not close turn: %#v", got)
			}
		})
	}
}

func TestToolPathsUseSessionWorkingDirectory(t *testing.T) {
	got, err := Parse("PostToolUse", []byte(`{"session_id":"s","cwd":"/repo/sub","tool_name":"write","tool_input":{"path":"file.go"}}`), "/repo")
	if err != nil || len(got) != 1 || len(got[0].Paths) != 1 || got[0].Paths[0] != "/repo/sub/file.go" {
		t.Fatalf("tool event: %+v, %v", got, err)
	}
}
