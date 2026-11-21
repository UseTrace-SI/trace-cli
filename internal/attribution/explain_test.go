package attribution

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

func TestResolvePath(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		path, want string
		ok         bool
	}{
		{"file.txt", "file.txt", true},
		{filepath.Join(root, "dir", "file.txt"), "dir/file.txt", true},
		{"../file.txt", "", false},
		{".env", "", false},
		{".git/config", "", false},
		{".trace/trace.db", "", false},
	} {
		got, err := ResolvePath(root, tc.path)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("ResolvePath(%q) = %q, %v; want %q, ok=%t", tc.path, got, err, tc.want, tc.ok)
		}
	}
}

func TestExplainFileAlignsWorkingCopy(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	db, err := store.Open(filepath.Join(root, ".trace", "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bs, err := blob.Open(filepath.Join(root, ".trace", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := bs.PutBytes([]byte("header\nold\nfooter\n"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := bs.PutBytes([]byte("header\nagent\nfooter\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex',?,1)", []any{root}},
		{"INSERT INTO turns(id,session_id,seq,prompt,started_at) VALUES('t','s',1,'edit',1)", nil},
		{"INSERT INTO ops(id,turn_id,path,kind,before_hash,after_hash,source,at) VALUES('o','t','f.txt','modify',?,?,'tool',2)", []any{string(before), string(after)}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("new\nheader\nagent\nfooter\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		line  int
		agent string
	}{{1, "unknown"}, {2, "baseline"}, {3, "codex"}, {4, "baseline"}} {
		got, err := ExplainFile(db, bs, root, "f.txt", tc.line, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Agent != tc.agent {
			t.Errorf("line %d = %+v; want %s", tc.line, got, tc.agent)
		}
	}
	if _, err := ExplainFile(db, bs, root, "f.txt", 5, false); err == nil {
		t.Fatal("line outside the file was explained")
	}
}
