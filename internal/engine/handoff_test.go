package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

const handoffSecret = "ghp_" + "a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6q7R8"

// handoffRepo records two claude-code turns: the first creates main.go,
// the second edits it and adds notes.md, ending with a "Next steps" list.
func handoffRepo(t *testing.T) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("README", "readme\n")
	e, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UnixMilli()
	turn := func(prompt, response string, edit func() []string) {
		t.Helper()
		ev := event.Event{Agent: "claude-code", AgentSessionID: "claude-sid-1", Worktree: root, At: at}
		ev.Kind, ev.Prompt = event.TurnStart, prompt
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
		paths := edit()
		ev.Kind, ev.Prompt, ev.ToolName, ev.Paths = event.ToolEdit, "", "Write", paths
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
		ev.Kind, ev.ToolName, ev.Paths, ev.ContextType, ev.Text = event.TurnContext, "", nil, "response", response
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
		ev.Kind, ev.ContextType, ev.Text = event.TurnEnd, "", ""
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
		at++
	}
	turn("Add a CLI entry point", "Created main.go.", func() []string {
		write("main.go", "package main\n\nfunc main() {}\n")
		return []string{filepath.Join(root, "main.go")}
	})
	turn("Print a greeting and keep notes", "Done.\n\nNext steps:\n- add tests for the greeting\n- [ ] update the docs\n\nTODO: handle --help\nThanks!", func() []string {
		write("main.go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hi\") }\n")
		write("notes.md", "notes\n")
		write(".env", "API_TOKEN="+handoffSecret+"\n")
		return []string{filepath.Join(root, "main.go"), filepath.Join(root, "notes.md"), filepath.Join(root, ".env")}
	})
	return e, root
}

func lastStep(t *testing.T, e *Engine) int64 {
	t.Helper()
	var step int64
	if err := e.DB.QueryRow("SELECT MAX(seq) FROM keyframes").Scan(&step); err != nil {
		t.Fatal(err)
	}
	return step
}

func TestHandoffContent(t *testing.T) {
	e, _ := handoffRepo(t)
	step := lastStep(t, e)
	h, err := e.Handoff(step, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sid string
	if err := e.DB.QueryRow("SELECT id FROM sessions").Scan(&sid); err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("SID", sid, "@STEP@", strconv.FormatInt(step, 10)).Replace(`# Trace handoff

You are continuing work recorded by Trace in a claude-code session. The working tree is at Trace step @STEP@. Review the summary below, then continue the task.

Source session: SID

## Original goal

Add a CLI entry point

## Turns (2)

- step 1: Add a CLI entry point
- step @STEP@: Print a greeting and keep notes

## Files changed (2)

- A main.go (+5 -0)
- A notes.md (+1 -0)

## Open items from the last response

- add tests for the greeting
- update the docs
- handle --help

## More history

Run ` + "`trace show @STEP@`" + ` for this step's turn and diff, ` + "`trace log`" + ` for all steps, and ` + "`trace diff STEP STEP`" + ` to compare steps. With the Trace MCP server, use get_turn, get_messages and search_history.
`)
	if h.Text != want {
		t.Fatalf("handoff text:\n%s\nwant:\n%s", h.Text, want)
	}
	if h.SessionID != sid || h.Agent != "claude-code" || h.Turns != 2 || h.Truncated ||
		!reflect.DeepEqual(h.Files, []string{"main.go", "notes.md"}) {
		t.Fatalf("handoff = %+v", h)
	}

	// Step 1 alone covers one turn and its own file.
	h, err = e.Handoff(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Text, "- A main.go (+3 -0)") || strings.Contains(h.Text, "notes.md") || strings.Contains(h.Text, "Open items") {
		t.Fatalf("step 1 handoff:\n%s", h.Text)
	}
}

func TestHandoffRedactsAndExcludesSecrets(t *testing.T) {
	e, _ := handoffRepo(t)
	step := lastStep(t, e)
	// Simulate history stored before a redaction rule existed.
	if _, err := e.DB.Exec("UPDATE turns SET prompt=? WHERE seq=1", "use token "+handoffSecret+" to deploy"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("UPDATE messages SET content=? WHERE role='assistant'", "Next steps:\n- rotate password="+handoffSecret); err != nil {
		t.Fatal(err)
	}
	h, err := e.Handoff(step, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.Text, handoffSecret) || strings.Contains(h.Text, handoffSecret[4:20]) {
		t.Fatalf("secret leaked:\n%s", h.Text)
	}
	if strings.Contains(h.Text, ".env") {
		t.Fatalf("excluded path listed:\n%s", h.Text)
	}
	if !strings.Contains(h.Text, "⟨redacted:") {
		t.Fatalf("no redaction marker:\n%s", h.Text)
	}
}

func TestHandoffIsCapped(t *testing.T) {
	e, _ := handoffRepo(t)
	step := lastStep(t, e)
	if _, err := e.DB.Exec("UPDATE turns SET prompt=? WHERE seq=1", strings.Repeat("goal ", 4000)); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{MinHandoffBytes, 1500, 4096} {
		h, err := e.Handoff(step, limit)
		if err != nil {
			t.Fatal(err)
		}
		// Prompts are clipped first; the whole text only past 4096 bytes.
		if len(h.Text) > limit || h.Truncated != (limit < 4096) || !strings.Contains(h.Text, "trace show") || (!h.Truncated && !strings.Contains(h.Text, "goal goal…")) {
			t.Fatalf("limit %d: %d bytes truncated=%v\n%s", limit, len(h.Text), h.Truncated, h.Text)
		}
	}
	h, err := e.Handoff(step, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Text) > DefaultHandoffBytes {
		t.Fatalf("default handoff is %d bytes", len(h.Text))
	}
}

func TestResolveResumeTarget(t *testing.T) {
	e, _ := handoffRepo(t)
	step := lastStep(t, e)
	var sid string
	if err := e.DB.QueryRow("SELECT id FROM sessions").Scan(&sid); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{sid, sid[:8], "claude-sid-1"} {
		got, err := e.ResolveResumeTarget(arg)
		if err != nil || got != step {
			t.Fatalf("%q: step=%d err=%v", arg, got, err)
		}
	}
	if got, err := e.ResolveResumeTarget("1"); err != nil || got != 1 {
		t.Fatalf("step: %d %v", got, err)
	}
	for _, arg := range []string{"nope", "", "%", "_"} {
		if _, err := e.ResolveResumeTarget(arg); err == nil {
			t.Fatalf("%q accepted", arg)
		}
	}
	if _, err := e.Handoff(999, 0); err == nil {
		t.Fatal("missing step accepted")
	}
}

func TestExtractTodos(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"", []string{}},
		{"All done.", []string{}},
		{"## Next steps\n1. one\n2) two\n\nOther text\n- not a todo", []string{"one", "two"}},
		{"- [x] done\n- [ ] open\n* TODO: later\nFIXME broken", []string{"open", "later", "broken"}},
		{"**Remaining work:**\n- a\n- a", []string{"a"}},
	} {
		if got := extractTodos(tc.text); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %q want %q", tc.text, got, tc.want)
		}
	}
}
