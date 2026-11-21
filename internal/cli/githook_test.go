package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/gitx"
)

// rewriteRepo is a real Git repository with Trace's hooks installed. Agent
// turns go through the engine; Git commands run the installed hooks, which
// run this test binary (see TestMain). watch records working-tree changes the
// way the daemon's file watcher would, including those Git itself makes.
type rewriteRepo struct {
	t    *testing.T
	root string
	e    *engine.Engine
}

func newRewriteRepo(t *testing.T, files map[string]string) *rewriteRepo {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
	t.Setenv("GIT_EDITOR", "true")
	t.Setenv("TRACE_HOME", t.TempDir())
	r := &rewriteRepo{t: t, root: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "commit.gpgsign", "false")
	for path, content := range files {
		r.write(path, content)
	}
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "base")
	r.initTrace()
	return r
}

// initTrace does what `trace init` does for history: exclude .trace, take a
// baseline and install the Git hooks.
func (r *rewriteRepo) initTrace() {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Join(r.root, ".trace"), 0700); err != nil {
		r.t.Fatal(err)
	}
	if err := addExclude(r.root); err != nil {
		r.t.Fatal(err)
	}
	e, err := engine.Open(r.root)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { e.Close() })
	if err := e.Baseline(); err != nil {
		r.t.Fatal(err)
	}
	if err := gitx.InstallHook(r.root); err != nil {
		r.t.Fatal(err)
	}
	r.e = e
}

func (r *rewriteRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *rewriteRepo) write(path, content string) {
	r.t.Helper()
	p := filepath.Join(r.root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rewriteRepo) read(path string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(path)))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

// agentTurn records one Codex turn that writes files.
func (r *rewriteRepo) agentTurn(prompt string, files map[string]string) {
	r.t.Helper()
	process := func(ev event.Event) {
		r.t.Helper()
		ev.Agent, ev.AgentSessionID, ev.Worktree, ev.At = "codex", "rewrite-session", r.root, time.Now().UnixMilli()
		if err := r.e.Process(ev); err != nil {
			r.t.Fatal(err)
		}
	}
	process(event.Event{Kind: event.SessionStart})
	process(event.Event{Kind: event.TurnStart, Prompt: prompt})
	var paths []string
	for path, content := range files {
		r.write(path, content)
		paths = append(paths, filepath.Join(r.root, filepath.FromSlash(path)))
	}
	process(event.Event{Kind: event.ToolEdit, ToolName: "Edit", Paths: paths})
	process(event.Event{Kind: event.TurnEnd})
}

// watch records the current content of paths as the file watcher would; with
// no open turn these changes are attributed to the user.
func (r *rewriteRepo) watch(paths ...string) {
	r.t.Helper()
	var absolute []string
	for _, path := range paths {
		absolute = append(absolute, filepath.Join(r.root, filepath.FromSlash(path)))
	}
	if err := r.e.RecordChanges(absolute); err != nil {
		r.t.Fatal(err)
	}
}

// sequence runs `git rebase -i` with a todo list chosen by the test.
func (r *rewriteRepo) sequence(upstream string, todo ...string) {
	r.t.Helper()
	dir := r.t.TempDir()
	list := filepath.Join(dir, "todo")
	if err := os.WriteFile(list, []byte(strings.Join(todo, "\n")+"\n"), 0600); err != nil {
		r.t.Fatal(err)
	}
	editor := filepath.Join(dir, "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\ncat '"+list+"' > \"$1\"\n"), 0700); err != nil {
		r.t.Fatal(err)
	}
	r.t.Setenv("GIT_SEQUENCE_EDITOR", editor)
	r.git("rebase", "-q", "-i", upstream)
}

func (r *rewriteRepo) hookErrors() {
	r.t.Helper()
	if b, err := os.ReadFile(filepath.Join(r.root, ".trace", "test-hook-errors")); err == nil {
		r.t.Fatalf("Git hook processing failed:\n%s", b)
	}
}

const baseGo = "package app\n\nfunc Base() int {\n\treturn 1\n}\n"

type commitRow struct {
	kind, from, patchID string
	attributed          bool
}

func (r *rewriteRepo) commitRow(sha string) commitRow {
	r.t.Helper()
	var row commitRow
	var attribution string
	if err := r.e.DB.QueryRow("SELECT COALESCE(rewrite_kind,''),COALESCE(rewritten_from,''),COALESCE(patch_id,''),COALESCE(attribution,'') FROM commits WHERE sha=?", sha).
		Scan(&row.kind, &row.from, &row.patchID, &attribution); err != nil {
		r.t.Fatalf("commit %s was not recorded: %v", sha, err)
	}
	row.attributed = strings.Contains(attribution, `"files"`)
	return row
}

// The post-commit and post-rewrite hooks report what Git did; the engine
// stores each commit's rewrite sources, patch ID and line attribution, and
// keeps post-rewrite pairs locally.
func TestGitHooksRecordCommitRewrites(t *testing.T) {
	r := newRewriteRepo(t, map[string]string{"app.go": baseGo})
	r.agentTurn("add Agent", map[string]string{"app.go": baseGo + "\nfunc Agent() {}\n"})
	r.git("commit", "-q", "-a", "-m", "agent")
	original := r.git("rev-parse", "HEAD")
	if row := r.commitRow(original); row.kind != "" || row.from != "" || row.patchID == "" || !row.attributed {
		t.Fatalf("plain commit recorded as %+v", row)
	}
	r.git("commit", "-q", "--amend", "-m", "agent, reworded")
	amended := r.git("rev-parse", "HEAD")
	if row := r.commitRow(amended); row.kind != "amend" || row.from != original || !row.attributed {
		t.Fatalf("amended commit recorded as %+v", row)
	}
	var pairs int
	if err := r.e.DB.QueryRow("SELECT COUNT(*) FROM commit_rewrites WHERE old_sha=? AND new_sha=? AND kind='amend'", original, amended).Scan(&pairs); err != nil || pairs != 1 {
		t.Fatalf("post-rewrite pair not recorded: %d, %v", pairs, err)
	}
	r.git("checkout", "-q", "-b", "topic")
	r.write("other.go", "package app\n")
	r.git("add", "other.go")
	r.git("commit", "-q", "-m", "other")
	topic := r.git("rev-parse", "HEAD")
	r.git("checkout", "-q", "main")
	r.write("main.txt", "main\n")
	r.git("add", "main.txt")
	r.git("commit", "-q", "-m", "main")
	r.git("cherry-pick", topic)
	picked := r.git("rev-parse", "HEAD")
	if row := r.commitRow(picked); row.kind != "cherry-pick" || row.from != topic || row.patchID != r.commitRow(topic).patchID {
		t.Fatalf("cherry-picked commit recorded as %+v", row)
	}
	r.git("checkout", "-q", "topic")
	r.write("more.go", "package app\n")
	r.git("add", "more.go")
	r.git("commit", "-q", "-m", "more")
	more := r.git("rev-parse", "HEAD")
	// The cherry-picked commit is already upstream, so the rebase drops it
	// and rewrites only the second one.
	r.git("rebase", "-q", "main")
	rebased := r.git("rev-parse", "HEAD")
	if rebased == more || r.git("rev-parse", "HEAD^") != picked {
		t.Fatal("rebase did not rewrite the topic commit onto main")
	}
	if row := r.commitRow(rebased); row.kind != "rebase" || row.from != more {
		t.Fatalf("rebased commit recorded as %+v", row)
	}
	if err := r.e.DB.QueryRow("SELECT COUNT(*) FROM commit_rewrites WHERE old_sha=? AND new_sha=? AND kind='rebase'", more, rebased).Scan(&pairs); err != nil || pairs != 1 {
		t.Fatalf("rebase pair not recorded: %d, %v", pairs, err)
	}
	r.hookErrors()
}
