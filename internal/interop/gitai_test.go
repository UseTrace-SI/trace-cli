package interop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/store"
)

// specNote follows the example in the Git AI Standard v3.0.0.
const specNote = `src/main.rs
  s_c9883b05a2487d::t_9f8e7d6c5b4a32 1-10,15-20
  h_31dce776f88375 11
"dir with space/a.go"
  0123456789abcdef 3
  s_ffffffffffffff::t_9f8e7d6c5b4a32 4
.env
  s_c9883b05a2487d::t_9f8e7d6c5b4a32 1
---
{"schema_version":"authorship/3.0.0","git_ai_version":"1.4.5","base_commit_sha":"7734793b756b3921c88db5375a8c156e9532447b",
"prompts":{"0123456789abcdef":{"agent_id":{"tool":"claude","id":"x","model":"claude-opus"},"total_additions":1,"total_deletions":0,"accepted_lines":1,"overriden_lines":0}},
"humans":{"h_31dce776f88375":{"author":"Developer <dev@example.com>"}},
"sessions":{"s_c9883b05a2487d":{"agent_id":{"tool":"cursor","id":"abc","model":"claude-sonnet-4-5-20250514"},"human_author":"dev@example.com"}}}
`

func TestParseGitAINote(t *testing.T) {
	hints, err := ParseGitAINote("c", []byte(specNote))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range hints {
		got = append(got, strings.Join([]string{h.Path, h.Contributor, h.Tool, h.Model, h.Session}, "|")+" "+strconv.Itoa(h.StartLine)+"-"+strconv.Itoa(h.EndLine))
	}
	want := []string{
		"src/main.rs|ai|cursor|claude-sonnet-4-5-20250514|s_c9883b05a2487d 1-10",
		"src/main.rs|ai|cursor|claude-sonnet-4-5-20250514|s_c9883b05a2487d 15-20",
		"src/main.rs|human|||h_31dce776f88375 11-11",
		"dir with space/a.go|ai|claude|claude-opus|0123456789abcdef 3-3",
		".env|ai|cursor|claude-sonnet-4-5-20250514|s_c9883b05a2487d 1-1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, bad := range []string{"no separator", "a\n---\n{\"schema_version\":\"authorship/2.0.0\"}", "---\nnot json"} {
		if _, err := ParseGitAINote("c", []byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=T",
		"GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func TestImportGitAIIsReadOnlyAndIdempotent(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "a.go")
	git(t, root, "commit", "-q", "-m", "one")
	db, err := store.Open(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if r, err := ImportGitAI(db.DB, root); err != nil || r.Notes != 0 {
		t.Fatalf("without notes: %+v %v", r, err)
	}
	git(t, root, "notes", "--ref=ai", "add", "-m", specNote, "HEAD")
	git(t, root, "notes", "--ref=ai", "add", "-m", "garbage", "HEAD~0^{tree}")
	before := git(t, root, "for-each-ref") + git(t, root, "notes", "--ref=ai", "list") + git(t, root, "log", "--all", "--format=%H %B")
	first, err := ImportGitAI(db.DB, root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Notes != 2 || first.Imported != 4 || len(first.Skipped) != 1 {
		t.Fatalf("first import: %+v", first)
	}
	second, err := ImportGitAI(db.DB, root)
	if err != nil || second.Imported != 0 || second.Existing != 4 {
		t.Fatalf("second import: %+v %v", second, err)
	}
	after := git(t, root, "for-each-ref") + git(t, root, "notes", "--ref=ai", "list") + git(t, root, "log", "--all", "--format=%H %B")
	if before != after {
		t.Fatalf("import changed the repository:\n%s\n%s", before, after)
	}
	head := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	hints, err := HintsFor(db, head, "src/main.rs")
	if err != nil || len(hints) != 3 {
		t.Fatalf("hints: %+v %v", hints, err)
	}
	if excluded, _ := HintsFor(db, head, ".env"); len(excluded) != 0 {
		t.Fatalf("stored excluded path: %+v", excluded)
	}
}
