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

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

func competitiveRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAgentRemoveUninstallsClaudeHooks(t *testing.T) {
	root := competitiveRepo(t)
	jsonOutput := false
	add := agentCommand(&root, &jsonOutput)
	add.SetArgs([]string{"add", "claude-code"})
	add.SetOut(&bytes.Buffer{})
	if err := add.Execute(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, ".claude", "settings.local.json")
	installed, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(installed), " hook claude-code") {
		t.Fatalf("hooks not installed: %v", err)
	}
	remove := agentCommand(&root, &jsonOutput)
	remove.SetArgs([]string{"remove", "claude-code"})
	remove.SetOut(&bytes.Buffer{})
	if err := remove.Execute(); err != nil {
		t.Fatal(err)
	}
	remaining, err := os.ReadFile(p)
	if err == nil && strings.Contains(string(remaining), " hook claude-code") {
		t.Fatal("agent remove left Claude hooks installed")
	}
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestGenericHookRedactsBeforeSpool(t *testing.T) {
	secret := "sk-abcdefghijklmnopqrstuvwxyz1234567890"
	body := `{"session_id":"s","prompt":"token ` + secret + `","text":"token ` + secret + `","paths":[".env","src/main.go"]}`
	ev, err := parseGenericHook("pi", "turn_context", []byte(body), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), `.env`) || len(ev.Paths) != 1 || ev.Paths[0] != "src/main.go" {
		t.Fatalf("unsafe generic event: %s", encoded)
	}
}

func TestAgentAddRemoveWindsurf(t *testing.T) {
	root := competitiveRepo(t)
	jsonOutput := false
	cmd := agentCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"add", "windsurf"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".devin", "hooks.json")
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), " hook windsurf post_cascade_response") {
		t.Fatalf("Windsurf hooks missing: %s, %v", b, err)
	}
	cmd = agentCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"remove", "windsurf"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Windsurf hooks remain: %v", err)
	}
}

func TestAgentAddRemoveGemini(t *testing.T) {
	root := competitiveRepo(t)
	jsonOutput := false
	cmd := agentCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"add", "gemini"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".gemini", "settings.json")
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), " hook gemini AfterAgent") {
		t.Fatalf("Gemini hooks missing: %s, %v", b, err)
	}
	cmd = agentCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"remove", "gemini"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Gemini hooks remain: %v", err)
	}
}

func TestAgentAddRemoveCopilot(t *testing.T) {
	root := competitiveRepo(t)
	jsonOutput := false
	cmd := agentCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"add", "copilot"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".github", "hooks", "usetrace.json")
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), " hook copilot agentStop") {
		t.Fatalf("Copilot hooks missing: %s, %v", b, err)
	}
	cmd = agentCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"remove", "copilot"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil || strings.Contains(string(b), " hook copilot ") {
		t.Fatalf("Copilot hooks remain: %s %v", b, err)
	}
}

func TestDispatchJSONIncludesHistory(t *testing.T) {
	root := competitiveRepo(t)
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	h, err := (manifest.Manifest{}).Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex',?,1)", root); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO turns(id,session_id,seq,prompt,reasoning,started_at) VALUES('t','s',1,'fix bug','found cause',1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO keyframes(id,turn_id,seq,manifest_hash,tag,created_at) VALUES('k','t',1,?,'turn',1)", h); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	jsonOutput := true
	cmd := dispatchCommand(&root, &jsonOutput)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []struct {
		Step   int64  `json:"step"`
		Agent  string `json:"agent"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Step != 1 || got[0].Agent != "codex" || got[0].Prompt != "fix bug" {
		t.Fatalf("dispatch JSON = %s", out.String())
	}
}

func TestCleanPreviewsWithoutDeleting(t *testing.T) {
	root := competitiveRepo(t)
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	h, err := (manifest.Manifest{}).Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) VALUES('k',1,?,'turn',1)", h); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	jsonOutput := false
	cmd := cleanCommand(&root, &jsonOutput)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Would remove 1") {
		t.Fatalf("unexpected preview: %q", out.String())
	}
	e, err = engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM keyframes WHERE id='k'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("clean preview removed history")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	cmd = cleanCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"--force"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	e, err = engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.DB.QueryRow("SELECT COUNT(*) FROM keyframes WHERE id='k'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("clean --force retained old unlinked history")
	}
}

func TestDoctorBundleDoesNotCopyDaemonLog(t *testing.T) {
	root := competitiveRepo(t)
	secret := "sk-abcdefghijklmnopqrstuvwxyz1234567890"
	if err := os.WriteFile(filepath.Join(root, ".trace", "daemon.log"), []byte("error: "+secret), 0600); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "doctor.json")
	jsonOutput := true
	cmd := doctorCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"--bundle", bundle})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) {
		t.Fatalf("doctor JSON is invalid: %q", out.String())
	}
	b, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), secret) || strings.Contains(string(b), "daemon_log_tail") {
		t.Fatalf("bundle contains daemon log: %s", b)
	}
}

func TestPluginDiscoverySkipsBuiltinAndNonExecutable(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	for _, file := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(first, "trace-example"), 0600},
		{filepath.Join(second, "trace-example"), 0700},
		{filepath.Join(second, "trace-search"), 0700},
	} {
		if err := os.WriteFile(file.path, []byte("#!/bin/sh\necho plugin\n"), file.mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)
	root := &cobra.Command{Use: "trace"}
	root.AddCommand(&cobra.Command{Use: "search"})
	loadPlugins(root)
	var searchCount, exampleCount int
	for _, cmd := range root.Commands() {
		switch cmd.Name() {
		case "search":
			searchCount++
		case "example":
			exampleCount++
		}
	}
	if searchCount != 1 || exampleCount != 1 {
		t.Fatalf("plugin commands: search=%d example=%d", searchCount, exampleCount)
	}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"example"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "plugin" {
		t.Fatalf("plugin output: %q", out.String())
	}
}

func TestAgentHelpJSON(t *testing.T) {
	jsonOutput := true
	cmd := agentHelpCommand(nil, &jsonOutput)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got["help"], "trace why") {
		t.Fatalf("missing command help: %s", out.String())
	}
}

func TestSearchMessagesAndEditedPaths(t *testing.T) {
	root := competitiveRepo(t)
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	h, err := (manifest.Manifest{}).Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex',?,1)", []any{root}},
		{"INSERT INTO turns(id,session_id,seq,prompt,reasoning,started_at) VALUES('t','s',1,'fix auth flow','diagnosed deadlock',1)", nil},
		{"INSERT INTO keyframes(id,turn_id,seq,manifest_hash,tag,created_at) VALUES('k','t',1,?,'turn',1)", []any{h}},
		{"INSERT INTO messages(id,session_id,turn_id,role,content,at) VALUES('m','s','t','assistant','sqlite panic during sign-in',1)", nil},
		{"INSERT INTO ops(id,turn_id,path,kind,source,at) VALUES('o','t','src/auth.go','modify','tool',1)", nil},
	} {
		if _, err := e.DB.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"default searches messages", []string{"panic"}, 1},
		{"messages only", []string{"--scope", "messages", "panic"}, 1},
		{"turns only", []string{"--scope", "turns", "panic"}, 0},
		{"and and or terms", []string{"--scope", "messages", "deadlock|panic", "sqlite"}, 1},
		{"punctuation is literal", []string{"sign-in"}, 1},
		{"path only", []string{"--edited-path", "src/**"}, 1},
		{"path and message", []string{"--edited-path", "src/**", "panic"}, 1},
		{"wrong path", []string{"--edited-path", "docs/**", "panic"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jsonOutput := true
			cmd := searchCommand(&root, &jsonOutput)
			cmd.SetArgs(tc.args)
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var got []map[string]any
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Fatalf("got %d results, want %d: %s", len(got), tc.want, out.String())
			}
		})
	}
}

func TestMessagesFilterByStepAndSession(t *testing.T) {
	root := competitiveRepo(t)
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	h, err := (manifest.Manifest{}).Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s1','codex',?,1),('s2','claude-code',?,2)", []any{root, root}},
		{"INSERT INTO turns(id,session_id,seq,started_at) VALUES('t1','s1',1,1),('t2','s2',2,2)", nil},
		{"INSERT INTO keyframes(id,turn_id,seq,manifest_hash,tag,created_at) VALUES('k1','t1',1,?,'turn',1),('k2','t2',2,?,'turn',2)", []any{h, h}},
		{"INSERT INTO messages(id,session_id,turn_id,role,content,at) VALUES('m1','s1','t1','user','first topic',1),('m2','s2','t2','assistant','second topic',2)", nil},
	} {
		if _, err := e.DB.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, flag, value, want string
	}{
		{"step", "--step", "1", "first topic"},
		{"session", "--session", "s2", "second topic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jsonOutput := true
			cmd := messagesCommand(&root, &jsonOutput)
			cmd.SetArgs([]string{tc.flag, tc.value})
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var got []struct {
				Content string `json:"content"`
				Step    int64  `json:"step"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Content != tc.want || got[0].Step < 1 {
				t.Fatalf("messages = %s", out.String())
			}
		})
	}
	jsonOutput := true
	cmd := messagesCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"--limit", "1", "--offset", "1"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var page []struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(out.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Content != "second topic" {
		t.Fatalf("message page = %s", out.String())
	}
}

func TestActivityCountsRecentLocalWork(t *testing.T) {
	root := competitiveRepo(t)
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	h, err := (manifest.Manifest{}).Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	old := now - (10 * 24 * time.Hour).Milliseconds()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s1','codex',?,?),('s2','claude-code',?,?)", []any{root, now, root, old}},
		{"INSERT INTO turns(id,session_id,seq,started_at) VALUES('t1','s1',1,?),('t2','s1',2,?),('t3','s2',3,?)", []any{now, now, old}},
		{"INSERT INTO keyframes(id,turn_id,seq,manifest_hash,tag,created_at) VALUES('k1','t1',1,?,'turn',?),('k2','t2',2,?,'turn',?),('k3','t3',3,?,'turn',?)", []any{h, now, h, now, h, old}},
		{"INSERT INTO ops(id,turn_id,path,kind,source,at) VALUES('o1','t1','a.go','modify','tool',?),('o2','t2','a.go','modify','tool',?),('o3','t2','b.go','modify','tool',?),('o4','t3','old.go','modify','tool',?)", []any{now, now, now, old}},
	} {
		if _, err := e.DB.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	jsonOutput := true
	cmd := activityCommand(&root, &jsonOutput)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got activityReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Turns != 2 || got.Sessions != 1 || got.FilesChanged != 2 || len(got.Agents) != 1 || got.Agents[0].Agent != "codex" || len(got.RecentSessions) != 1 {
		t.Fatalf("activity = %s", out.String())
	}
}

func TestWhyIncludesConversation(t *testing.T) {
	root := competitiveRepo(t)
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("new line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := e.Blobs.PutBytes([]byte("old line\n"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := e.Blobs.PutBytes([]byte("new line\n"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := (manifest.Manifest{}).Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex',?,1)", []any{root}},
		{"INSERT INTO turns(id,session_id,seq,prompt,started_at) VALUES('t','s',1,'change the line',1)", nil},
		{"INSERT INTO keyframes(id,turn_id,seq,manifest_hash,tag,created_at) VALUES('k','t',1,?,'turn',2)", []any{h}},
		{"INSERT INTO ops(id,turn_id,path,kind,before_hash,after_hash,source,at) VALUES('o','t','file.txt','modify',?,?,'tool',2)", []any{before, after}},
		{"INSERT INTO messages(id,session_id,turn_id,role,content,at) VALUES('m1','s','t','user','please change it',1),('m2','s','t','assistant','done',2)", nil},
	} {
		if _, err := e.DB.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	jsonOutput := true
	cmd := whyCommand(&root, &jsonOutput)
	cmd.SetArgs([]string{"--messages", "file.txt:1"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []whyResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Step != 1 || len(got[0].Messages) != 2 || got[0].Messages[0].Content != "please change it" {
		t.Fatalf("why conversation = %s", out.String())
	}
}

func TestTUIWhyInputAndReturn(t *testing.T) {
	m := tuiModel{view: 0}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(tuiModel)
	if m.view != 3 {
		t.Fatalf("w opened view %d, want why input", m.view)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("file.txt:1")})
	m = updated.(tuiModel)
	if m.whyInput != "file.txt:1" {
		t.Fatalf("why input = %q", m.whyInput)
	}
	updated, _ = m.Update(tuiWhyMsg{text: "Step 1: codex"})
	m = updated.(tuiModel)
	if m.view != 4 || !strings.Contains(m.View(), "Step 1") {
		t.Fatalf("why result view = %q", m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(tuiModel)
	if m.view != 0 {
		t.Fatalf("escape returned to view %d", m.view)
	}
}

func TestTUIDiffShowsSelectedStep(t *testing.T) {
	root := competitiveRepo(t)
	p := filepath.Join(root, "file.txt")
	if err := os.WriteFile(p, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Scan(root, nil, e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	h, err := m.Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) VALUES('step1',1,?,'turn',1)", h); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	model := tuiModel{root: root, view: 1, steps: []tuiStep{{seq: 1}}}
	_, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if command == nil {
		t.Fatal("diff command missing")
	}
	message, ok := command().(tuiDiffMsg)
	if !ok || !strings.Contains(message.text, "-before") || !strings.Contains(message.text, "+after") {
		t.Fatalf("diff message = %#v", message)
	}
}
