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
		for _, kind := range []ResumeTargetKind{ResumeAuto, ResumeSession} {
			got, err := e.ResolveResumeTarget(arg, kind)
			if err != nil || got != step {
				t.Fatalf("%q: step=%d err=%v", arg, got, err)
			}
		}
	}
	if got, err := e.ResolveResumeTarget("1", ResumeAuto); err != nil || got != 1 {
		t.Fatalf("step: %d %v", got, err)
	}
	for _, tc := range []struct {
		arg  string
		kind ResumeTargetKind
	}{{"nope", ResumeAuto}, {"", ResumeAuto}, {"%", ResumeAuto}, {"_", ResumeSession}, {"x", ResumeStep}, {"1", ResumeSession}} {
		if _, err := e.ResolveResumeTarget(tc.arg, tc.kind); err == nil {
			t.Fatalf("%q (%d) accepted", tc.arg, tc.kind)
		}
	}
	if _, err := e.Handoff(999, 0); err == nil {
		t.Fatal("missing step accepted")
	}
}

func TestResolveResumeTargetSessionNames(t *testing.T) {
	e, _ := handoffRepo(t)
	var turnID string
	if err := e.DB.QueryRow("SELECT id FROM turns WHERE seq=1").Scan(&turnID); err != nil {
		t.Fatal(err)
	}
	// Sessions named with LIKE metacharacters, a numeric ID, and an agent
	// session ID that prefixes another session's ID.
	sessions := []struct{ id, agentSID string }{
		{"sess_abc123", "a1"}, {"sessXabc999", "a2"}, {"12345", "a3"}, {"100", "agentsid"}, {"agentsid-other", "a5"},
	}
	for i, s := range sessions {
		if _, err := e.DB.Exec("INSERT INTO sessions(id,agent,agent_sid,worktree,started_at) VALUES(?,?,?,?,1)", s.id, "codex", s.agentSID, "/x"); err != nil {
			t.Fatal(err)
		}
		seq := int64(100 + i)
		tid := "t-" + s.id
		if _, err := e.DB.Exec("INSERT INTO turns(id,session_id,seq,prompt,started_at) VALUES(?,?,?,?,1)", tid, s.id, seq, "p"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.DB.Exec("INSERT INTO keyframes(id,turn_id,seq,manifest_hash,created_at) SELECT 'k-'||?,?,?,manifest_hash,1 FROM keyframes WHERE seq=1", s.id, tid, seq); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		arg  string
		kind ResumeTargetKind
		want int64
		err  string
	}{
		{"sess_abc123", ResumeAuto, 100, ""},
		{"sess_abc", ResumeAuto, 100, ""}, // "_" is literal, not a wildcard
		{"sess%", ResumeAuto, 0, "no session matches"},
		{"sess", ResumeAuto, 0, "matches several sessions"},
		{"1234", ResumeAuto, 0, "ambiguous: \"1234\" is step 1234 or a session ID (or prefix of one); use --step or --session"},
		{"100", ResumeAuto, 0, "ambiguous: \"100\" is step 100"}, // an exact session ID
		{"12", ResumeAuto, 12, ""}, // too short to be read as a prefix
		{"123", ResumeStep, 123, ""},
		{"123", ResumeSession, 102, ""},
		{"2", ResumeAuto, 2, ""},
		{"agentsid", ResumeSession, 103, ""}, // exact agent session ID wins over an ID prefix
	} {
		got, err := e.ResolveResumeTarget(tc.arg, tc.kind)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%q: err = %v, want %q", tc.arg, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q (%d): got %d, %v; want %d", tc.arg, tc.kind, got, err, tc.want)
		}
	}
	// An ID that is also another session's agent session ID is reported.
	if _, err := e.DB.Exec("UPDATE sessions SET agent_sid='sess_abc123' WHERE id='12345'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ResolveResumeTarget("sess_abc123", ResumeSession); err == nil || !strings.Contains(err.Error(), "names several sessions (12345, sess_abc123)") {
		t.Fatalf("err = %v", err)
	}
}

// Fields are redacted before they are clipped, so a cut that would hide
// the end of a secret pattern cannot leak its start.
func TestHandoffRedactsBeforeClipping(t *testing.T) {
	e, _ := handoffRepo(t)
	step := lastStep(t, e)
	body := strings.Repeat("MIIEowIBAAKCAQEAx9", 80)
	pem := "-----BEGIN RSA PRIVATE KEY-----\n" + body + "\n-----END RSA PRIVATE KEY-----"
	aws := "AKIA" + "Z7Q4W8E2R6T1Y3U5"
	straddle := strings.Repeat("a", handoffPromptRunes-6) + " " + aws + " tail"
	if _, err := e.DB.Exec("UPDATE turns SET prompt=? WHERE seq=1", "deploy with "+pem); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("UPDATE turns SET prompt=? WHERE seq=?", straddle, step); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("UPDATE messages SET content=? WHERE role='assistant' AND turn_id=(SELECT id FROM turns WHERE seq=?)", "Next steps:\n- rotate "+strings.Repeat("b", handoffTodoRunes-8)+" "+aws, step); err != nil {
		t.Fatal(err)
	}
	h, err := e.Handoff(step, 0)
	if err != nil {
		t.Fatal(err)
	}
	all := h.Text + strings.Join(h.Todos, "\n") + strings.Join(h.Files, "\n")
	for _, leak := range []string{body[:24], "AKIAZ", "Z7Q4W8"} {
		if strings.Contains(all, leak) {
			t.Fatalf("%q leaked:\n%s", leak, all)
		}
	}
}

func TestHandoffRedactsFileNames(t *testing.T) {
	e, root := handoffRepo(t)
	name := "keys-" + handoffSecret + ".txt"
	if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ev := event.Event{Agent: "claude-code", AgentSessionID: "claude-sid-1", Worktree: root, At: time.Now().UnixMilli() + 10}
	for _, k := range []event.Kind{event.TurnStart, event.ToolEdit, event.TurnEnd} {
		ev.Kind = k
		ev.Prompt, ev.Paths = "", nil
		if k == event.TurnStart {
			ev.Prompt = "add key file"
		}
		if k == event.ToolEdit {
			ev.ToolName, ev.Paths = "Write", []string{filepath.Join(root, name)}
		}
		if err := e.Process(ev); err != nil {
			t.Fatal(err)
		}
	}
	h, err := e.Handoff(lastStep(t, e), 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.Text+strings.Join(h.Files, " "), handoffSecret) || len(h.Files) != 3 {
		t.Fatalf("files %q\n%s", h.Files, h.Text)
	}
}

func TestSanitizeHandoff(t *testing.T) {
	in := "a\x00b\x1b[31mred\x1b[0m\x1b]0;title\x07c\r\n\td\x7f\u009be"
	if got := sanitizeHandoff(in); got != "abredc\n\tde" {
		t.Fatalf("got %q", got)
	}
	e, _ := handoffRepo(t)
	if _, err := e.DB.Exec("UPDATE turns SET prompt=? WHERE seq=1", "evil\x00\x1b[2Jprompt"); err != nil {
		t.Fatal(err)
	}
	h, err := e.Handoff(lastStep(t, e), 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(h.Text, "\x00\x1b\r") || !strings.Contains(h.Text, "evilprompt") {
		t.Fatalf("unsanitized:\n%q", h.Text)
	}
}

func TestTruncateKeepingMarkers(t *testing.T) {
	m := "⟨redacted:github-token:0123abcd⟩"
	s := "abc " + m + " end"
	for n := 0; n <= len(s); n++ {
		got := truncateKeepingMarkers(s, n)
		if len(got) > n || !strings.HasPrefix(s, got) {
			t.Fatalf("n=%d: %q", n, got)
		}
		if i := strings.Index(got, "⟨"); i >= 0 && !strings.Contains(got, m) {
			t.Fatalf("n=%d split the marker: %q", n, got)
		}
	}
	if got := truncateKeepingMarkers(s, 6); got != "abc " {
		t.Fatalf("got %q", got)
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
