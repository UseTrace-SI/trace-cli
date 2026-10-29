package cline

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func TestInstallPreservesUserHook(t *testing.T) {
	root := t.TempDir()
	p := path(root, "TaskStart")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("user hook"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("replaced user hook")
	}
	if _, err := os.Stat(path(root, "TaskComplete")); !os.IsNotExist(err) {
		t.Fatalf("partial install: %v", err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	if !HasProjectHook(root, "TaskStart") {
		t.Fatal("project hook not detected")
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), marker) || !strings.Contains(string(b), `{"cancel":false}`) {
		t.Fatalf("hook %s %v", b, err)
	}
	info, err := os.Stat(p)
	if err != nil || info.Mode()&0100 == 0 {
		t.Fatalf("hook not executable: %v", err)
	}
	if err := Uninstall(root); err != nil {
		t.Fatal(err)
	}
	if HasProjectHook(root, "TaskStart") {
		t.Fatal("removed project hook still detected")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("hook remains: %v", err)
	}
}

func TestGlobalInstallRespectsConfigAndExistingHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLINE_DIR", dir)
	p := filepath.Join(dir, "hooks", "TaskStart")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("user hook"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(t.TempDir()); err == nil {
		t.Fatal("replaced user hook")
	}
	if b, err := os.ReadFile(p); err != nil || string(b) != "user hook" {
		t.Fatalf("user hook changed: %q, %v", b, err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "hook cline TaskStart") || strings.Contains(string(b), "--repo") {
		t.Fatalf("global hook: %s, %v", b, err)
	}
	if err := UninstallGlobal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("global hook remains: %v", err)
	}
}

func TestManagedHooksPreserveEditsAndAllowRefresh(t *testing.T) {
	root := filepath.Join(t.TempDir(), "a'project")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	p := path(root, "TaskStart")
	old := hookBody("/old/path/trace", root, "TaskStart")
	if err := os.WriteFile(p, []byte(old), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err != nil {
		t.Fatalf("refreshing managed hook: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) == old || !managedHook(got, "TaskStart") {
		t.Fatalf("hook was not refreshed: %v", err)
	}
	modified := string(got) + "# user edit\n"
	if err := os.WriteFile(p, []byte(modified), 0700); err != nil {
		t.Fatal(err)
	}
	if HasProjectHook(root, "TaskStart") {
		t.Fatal("modified project hook suppressed global capture")
	}
	if err := os.WriteFile(p, []byte(hookBody("/usr/local/bin/trace", "/old/repository", "TaskStart")), 0700); err != nil {
		t.Fatal(err)
	}
	if HasProjectHook(root, "TaskStart") {
		t.Fatal("stale project hook suppressed global capture")
	}
	if err := os.WriteFile(p, []byte(modified), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("replaced edited hook")
	}
	if err := Uninstall(root); err == nil {
		t.Fatal("removed edited hook")
	}
	got, err = os.ReadFile(p)
	if err != nil || string(got) != modified {
		t.Fatalf("edited hook changed: %v", err)
	}
	if _, err := os.Stat(path(root, "TaskComplete")); err != nil {
		t.Fatalf("uninstall partially removed hooks: %v", err)
	}
}

func TestManagedHookRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	p := path(root, "TaskStart")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "user-hook")
	if err := os.WriteFile(target, []byte(hookBody("/bin/trace", root, "TaskStart")), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	if err := Install(root); err == nil {
		t.Fatal("install accepted symlink")
	}
	if err := Uninstall(root); err == nil {
		t.Fatal("uninstall accepted symlink")
	}
}

func TestWorkspaceRelativeToolPath(t *testing.T) {
	for _, tt := range []struct {
		name, body, path string
	}{
		{"nested", `{"taskId":"s","workspaceRoots":["/repo/sub"],"postToolUse":{"toolName":"write_to_file","success":true,"parameters":{"path":"main.go"}}}`, "/repo/sub/main.go"},
		{"absolute", `{"taskId":"s","workspaceRoots":["/repo/sub","/repo/other"],"postToolUse":{"toolName":"read_file","success":true,"parameters":{"path":"/repo/main.go"}}}`, "/repo/main.go"},
		{"ambiguous", `{"taskId":"s","workspaceRoots":["/repo/sub","/repo/other"],"postToolUse":{"toolName":"write_to_file","success":true,"parameters":{"path":"main.go"}}}`, ""},
		{"invalid workspace", `{"taskId":"s","workspaceRoots":["relative"],"postToolUse":{"toolName":"write_to_file","success":true,"parameters":{"path":"main.go"}}}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse("PostToolUse", []byte(tt.body), "/repo")
			if err != nil || len(got) != 1 {
				t.Fatalf("events: %+v, %v", got, err)
			}
			if tt.path == "" && len(got[0].Paths) != 0 || tt.path != "" && (len(got[0].Paths) != 1 || got[0].Paths[0] != tt.path) {
				t.Fatalf("paths: %v, want %q", got[0].Paths, tt.path)
			}
		})
	}
}

func TestMultiFileReads(t *testing.T) {
	for _, tt := range []struct {
		name, params string
		want         []string
	}{
		{"file requests", `{"files":[{"path":"a.go"},{"file_path":"b.go"},{"filePath":"a.go"},{"path":"cline://cache/secret"}]}`, []string{"/repo/sub/a.go", "/repo/sub/b.go"}},
		{"alternate lists", `{"file_paths":["a.go","b.go"],"paths":["b.go","c.go"]}`, []string{"/repo/sub/b.go", "/repo/sub/c.go", "/repo/sub/a.go"}},
		{"bare array", `["a.go",{"path":"b.go"}]`, []string{"/repo/sub/a.go", "/repo/sub/b.go"}},
		{"bare string", `"a.go"`, []string{"/repo/sub/a.go"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"taskId":"s","workspaceRoots":["/repo/sub"],"postToolUse":{"toolName":"read_files","success":true,"parameters":` + tt.params + `}}`
			got, err := Parse("PostToolUse", []byte(body), "/repo")
			if err != nil || len(got) != 1 || got[0].Kind != event.FileRead || !slices.Equal(got[0].Paths, tt.want) {
				t.Fatalf("events: %+v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestTaskStartInitialPrompt(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	body := `{"taskId":"s","taskStart":{"taskMetadata":{"initialTask":"Fix main ` + secret + `"}}}`
	got, err := Parse("TaskStart", []byte(body), "/repo")
	if err != nil || len(got) != 2 || got[0].Kind != event.SessionStart || got[1].Kind != event.TurnStart || !got[1].InitialTurnOnly || !strings.Contains(got[1].Prompt, "Fix main") || strings.Contains(got[1].Prompt, secret) {
		t.Fatalf("task start: %+v, %v", got, err)
	}
	got, err = Parse("TaskResume", []byte(body), "/repo")
	if err != nil || len(got) != 1 || got[0].Kind != event.SessionStart {
		t.Fatalf("resume: %+v, %v", got, err)
	}
}

func TestFailedTools(t *testing.T) {
	for _, tool := range []string{"write_to_file", "editor", "apply_patch", "run_commands", "execute_command", "bash"} {
		t.Run(tool, func(t *testing.T) {
			body := `{"taskId":"s","postToolUse":{"toolName":"` + tool + `","success":false,"parameters":{"path":"a.go"}}}`
			got, err := Parse("PostToolUse", []byte(body), "/repo")
			if err != nil || len(got) != 1 || got[0].Kind != event.ToolEdit || len(got[0].Paths) != 0 {
				t.Fatalf("failed tool: %+v, %v", got, err)
			}
		})
	}
	got, err := Parse("PostToolUse", []byte(`{"taskId":"s","postToolUse":{"toolName":"read_files","success":false,"parameters":{"files":[{"path":"a.go"}]}}}`), "/repo")
	if err != nil || len(got) != 0 {
		t.Fatalf("failed read: %+v, %v", got, err)
	}
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kind       event.Kind
	}{
		{"TaskStart", `{"taskId":"s"}`, event.SessionStart},
		{"UserPromptSubmit", `{"taskId":"s","userPromptSubmit":{"prompt":"hello"}}`, event.TurnStart},
		{"PostToolUse", `{"taskId":"s","postToolUse":{"toolName":"write_to_file","success":true,"parameters":{"path":"a.go"}}}`, event.ToolEdit},
		{"PostToolUse mixed parameters", `{"taskId":"s","postToolUse":{"toolName":"write_to_file","success":true,"parameters":{"path":"a.go","recursive":false,"count":4,"metadata":{"nested":true}}}}`, event.ToolEdit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := strings.TrimSuffix(tc.name, " mixed parameters")
			got, err := Parse(name, []byte(tc.body), "/repo")
			if err != nil || len(got) != 1 || got[0].Kind != tc.kind || got[0].Agent != "cline" {
				t.Fatalf("events: %+v, %v", got, err)
			}
			if name == "PostToolUse" && (len(got[0].Paths) != 1 || got[0].Paths[0] != "a.go") {
				t.Fatalf("tool path missing: %+v", got)
			}
		})
	}
	got, err := Parse("TaskComplete", []byte(`{"taskId":"s","parent_agent_id":"parent"}`), "/repo")
	if err != nil || len(got) != 0 {
		t.Fatalf("subagent: %+v, %v", got, err)
	}
}

func TestFinalEventsRequestScanAndCloseOpenTurns(t *testing.T) {
	for _, name := range []string{"TaskComplete", "TaskError", "TaskCancel", "SessionShutdown"} {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(name, []byte(`{"taskId":"s"}`), "/repo")
			want := 2
			if name == "SessionShutdown" {
				want = 3
			}
			if err != nil || len(got) != want || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnEnd {
				t.Fatalf("events: %+v, %v", got, err)
			}
			if want == 3 && got[2].Kind != event.SessionEnd {
				t.Fatalf("session not closed: %+v", got)
			}
		})
	}
}

func TestCompletionCapturesRedactedResponse(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	body := `{"taskId":"s","turn":{"outputText":"Done ` + secret + `"}}`
	got, err := Parse("TaskComplete", []byte(body), "/repo")
	if err != nil || len(got) != 3 || got[0].Kind != event.ToolEdit || got[1].Kind != event.TurnContext || got[1].ContextType != "response" || got[2].Kind != event.TurnEnd {
		t.Fatalf("completion = %+v, %v", got, err)
	}
	if strings.Contains(got[1].Text, secret) || !strings.Contains(got[1].Text, "redacted") {
		t.Fatalf("response was not redacted: %q", got[1].Text)
	}
}
