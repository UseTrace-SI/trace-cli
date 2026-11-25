//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

const resumeSecret = "ghp_" + "Z9y8X7w6V5u4T3s2R1q0P9o8N7m6L5k4J3i2"

// resumeRepo records one claude-code session with two turns and returns
// the repository root and the session ID. CLAUDE.md and AGENTS.md exist so
// tests can check they are never modified.
func resumeRepo(t *testing.T) (string, string) {
	t.Helper()
	root := competitiveRepo(t)
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("CLAUDE.md", "project rules\n")
	write("AGENTS.md", "agent rules\n")
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UnixMilli()
	for i, prompt := range []string{"Start the parser", "Finish the parser"} {
		ev := event.Event{Agent: "claude-code", AgentSessionID: "cc-1", Worktree: root, At: at + int64(i)}
		ev.Kind, ev.Prompt = event.TurnStart, prompt
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
		write("parser.go", strings.Repeat("// line\n", i+1))
		ev.Kind, ev.Prompt, ev.ToolName, ev.Paths = event.ToolEdit, "", "Write", []string{filepath.Join(root, "parser.go")}
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
		ev.Kind, ev.ToolName, ev.Paths, ev.ContextType, ev.Text = event.TurnContext, "", nil, "response", "Next steps:\n- handle errors"
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
		ev.Kind, ev.ContextType, ev.Text = event.TurnEnd, "", ""
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
	}
	// History stored before a redaction rule existed.
	if _, err := e.DB.Exec("UPDATE turns SET prompt=? WHERE seq=1", "Start the parser with token "+resumeSecret); err != nil {
		t.Fatal(err)
	}
	var sid string
	if err := e.DB.QueryRow("SELECT id FROM sessions").Scan(&sid); err != nil {
		t.Fatal(err)
	}
	return root, sid
}

// stubLaunchers puts a stub for every launcher program on PATH. Each
// writes its arguments, NUL-terminated, to $dir/<program>.args.
func stubLaunchers(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, program := range []string{"agent", "agy", "claude", "codex"} {
		script := "#!/bin/sh\nprintf '%s\\0' \"$@\" > '" + filepath.Join(dir, program+".args") + "'\n"
		if err := os.WriteFile(filepath.Join(dir, program), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")) // stubs shadow real agents
	return dir
}

func stubArgs(t *testing.T, dir, program string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, program+".args"))
	if err != nil {
		t.Fatalf("%s was not launched: %v", program, err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
}

// worktreeFiles maps every file outside .git and .trace to its content.
func worktreeFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() && (rel == ".git" || rel == ".trace") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func runResumeCmd(t *testing.T, root string, jsonOutput bool, args ...string) (string, error) {
	t.Helper()
	cmd := resumeCommand(&root, &jsonOutput)
	cmd.SilenceUsage = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// Every launch-capable agent receives the handoff as its prompt argument,
// per its convention, and no file outside .trace changes.
func TestResumeLaunchesEachAgentWithHandoff(t *testing.T) {
	for _, tc := range []struct {
		agent, program string
		prefix         []string
	}{
		{"claude-code", "claude", nil},
		{"codex", "codex", nil},
		{"cursor", "agent", nil},
		{"agy", "agy", []string{"--prompt-interactive"}},
		{"", "claude", nil}, // the session's own agent
	} {
		t.Run(tc.program+"/"+tc.agent, func(t *testing.T) {
			root, sid := resumeRepo(t)
			stubs := stubLaunchers(t)
			before := worktreeFiles(t, root)
			args := []string{sid[:10], "--no-rewind"}
			if tc.agent != "" {
				args = append(args, "--agent", tc.agent)
			}
			out, err := runResumeCmd(t, root, false, args...)
			if err != nil {
				t.Fatalf("resume: %v\n%s", err, out)
			}
			got := stubArgs(t, stubs, tc.program)
			context, err := os.ReadFile(filepath.Join(root, ".trace", "context.md"))
			if err != nil {
				t.Fatal(err)
			}
			if want := append(append([]string{}, tc.prefix...), string(context)); !reflect.DeepEqual(got, want) {
				t.Fatalf("argv = %q, want %q", got, want)
			}
			for _, s := range []string{"in a claude-code session", "Start the parser", "- step 2: Finish the parser", "A parser.go (+2 -0)", "- handle errors", "trace show 2"} {
				if !strings.Contains(string(context), s) {
					t.Errorf("handoff lacks %q:\n%s", s, context)
				}
			}
			if strings.Contains(string(context), resumeSecret) || strings.Contains(strings.Join(got, " "), resumeSecret) || strings.Contains(out, resumeSecret) {
				t.Fatalf("secret leaked into the handoff")
			}
			if after := worktreeFiles(t, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("worktree changed:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func TestResumeRewindsBeforeLaunch(t *testing.T) {
	root, _ := resumeRepo(t)
	stubs := stubLaunchers(t)
	out, err := runResumeCmd(t, root, false, "1", "--agent", "codex")
	if err != nil {
		t.Fatalf("resume: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "parser.go")); string(b) != "// line\n" {
		t.Fatalf("parser.go = %q", b)
	}
	if got := stubArgs(t, stubs, "codex"); len(got) != 1 || !strings.Contains(got[0], "Trace step 1") || strings.Contains(got[0], "Finish the parser") {
		t.Fatalf("codex argv = %q", got)
	}
	if !strings.Contains(out, "undo with trace rewind") || !strings.Contains(out, "Launching codex: codex <handoff context>") {
		t.Fatalf("output:\n%s", out)
	}
	for _, rel := range []string{"CLAUDE.md", "AGENTS.md"} {
		if b, _ := os.ReadFile(filepath.Join(root, rel)); !strings.HasSuffix(string(b), "rules\n") || strings.Contains(string(b), "Trace") {
			t.Fatalf("%s modified: %q", rel, b)
		}
	}
}

func TestResumeNoLaunchPrintsPlan(t *testing.T) {
	root, sid := resumeRepo(t)
	stubs := stubLaunchers(t)
	out, err := runResumeCmd(t, root, false, sid, "--agent", "agy", "--no-rewind", "--no-launch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stubs, "agy.args")); err == nil {
		t.Fatal("--no-launch started the agent")
	}
	for _, s := range []string{"Resuming from step 2 without rewinding", "Would launch agy: agy --prompt-interactive <handoff context>", "# Trace handoff", "Finish the parser"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
	out, err = runResumeCmd(t, root, true, sid, "--agent", "codex", "--no-rewind", "--no-launch")
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if plan["mode"] != "in-place" || plan["agent"] != "codex" || plan["source_agent"] != "claude-code" || plan["session_id"] != sid ||
		plan["launched"] != false || !strings.Contains(plan["context"].(string), "Finish the parser") ||
		!reflect.DeepEqual(plan["command"], []any{"codex", "<handoff context>"}) {
		t.Fatalf("plan = %v", plan)
	}
	if strings.Contains(out, resumeSecret) {
		t.Fatal("secret leaked into JSON")
	}
}

// An agent without a launch capability is refused before the tree changes.
func TestResumeRefusesAgentWithoutLauncher(t *testing.T) {
	root, _ := resumeRepo(t)
	stubLaunchers(t)
	before := worktreeFiles(t, root)
	for _, agent := range []string{"gemini", "nope"} {
		_, err := runResumeCmd(t, root, false, "1", "--agent", agent)
		if err == nil || !strings.Contains(err.Error(), "cannot be launched; agents with a launch capability: agy, claude-code, codex, cursor") {
			t.Fatalf("%s: err = %v", agent, err)
		}
	}
	if after := worktreeFiles(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("refused resume changed the worktree")
	}
	for _, args := range [][]string{{"1", "--fork", "--no-rewind"}, {"1", "--name", "x"}, {"1", "--max-context-bytes", "10"}, {"zzz"}} {
		if _, err := runResumeCmd(t, root, false, args...); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}
