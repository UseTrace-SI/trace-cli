package attribution

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

type recordedOp struct {
	turn, before, after string
	at                  int64
}

func historyFixture(t *testing.T, baseline map[string]string, ops []recordedOp) (*store.DB, *blob.Store) {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	db, err := store.Open(filepath.Join(root, ".trace", "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bs, err := blob.Open(filepath.Join(root, ".trace", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	put := func(text string) string {
		if text == "" {
			return ""
		}
		h, err := bs.PutBytes([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return string(h)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex','/repo',1)")
	turns := map[string]bool{}
	for i, op := range ops {
		if op.turn != "" && !turns[op.turn] {
			turns[op.turn] = true
			exec("INSERT INTO turns(id,session_id,seq,prompt,started_at) VALUES(?,?,?,?,?)", op.turn, "s", len(turns), "prompt "+op.turn, op.at)
		}
		var turn any
		if op.turn != "" {
			turn = op.turn
		}
		exec("INSERT INTO ops(id,turn_id,path,kind,before_hash,after_hash,source,at) VALUES(?,?,?,?,?,?,?,?)",
			fmt.Sprintf("op%02d", i), turn, "f.go", "modify", put(op.before), put(op.after), "watcher", op.at)
	}
	if baseline != nil {
		m := manifest.Manifest{}
		for path, text := range baseline {
			m[path] = manifest.Entry{Hash: blob.Hash(put(text)), Mode: 0644}
		}
		h, err := m.Save(bs)
		if err != nil {
			t.Fatal(err)
		}
		exec("INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) VALUES('base',0,?,'baseline',0)", string(h))
	}
	return db, bs
}

func TestWalkAttribution(t *testing.T) {
	gofmtBefore := "x\nfunc f() {\n    y := 1\n}\n"
	gofmtAfter := "x\nfunc f() {\n\ty := 1\n}\n"
	for _, tc := range []struct {
		name     string
		baseline map[string]string
		ops      []recordedOp
		current  string
		line     int
		all      bool
		want     []string // "kind:turn:confidence" per reported origin
	}{
		{"checkout away and back keeps the author", nil, []recordedOp{
			{"t1", "a\n", "a\nagent\n", 1}, {"", "a\nagent\n", "a\n", 2}, {"", "a\n", "a\nagent\n", 3},
		}, "a\nagent\n", 2, false, []string{"turn:t1:exact"}},
		{"uncaptured revert of a later edit", nil, []recordedOp{
			{"t1", "a\n", "a\nagent\n", 1}, {"", "a\nagent\n", "a\nagent\nhuman\n", 2},
		}, "a\nagent\n", 2, false, []string{"turn:t1:exact"}},
		{"recorded reformat keeps the author", nil, []recordedOp{
			{"t1", "x\n", gofmtBefore, 1}, {"", gofmtBefore, gofmtAfter, 2},
		}, gofmtAfter, 3, false, []string{"turn:t1:normalized"}},
		{"all lists the reformat", nil, []recordedOp{
			{"t1", "x\n", gofmtBefore, 1}, {"", gofmtBefore, gofmtAfter, 2},
		}, gofmtAfter, 3, true, []string{"human::exact", "turn:t1:normalized"}},
		{"uncaptured reformat keeps the author", nil, []recordedOp{
			{"t1", "x\n", "x\ncall( 'a' )\n", 1},
		}, "x\ncall(\"a\");\n", 2, false, []string{"turn:t1:normalized"}},
		{"semantic edit is not a reformat", nil, []recordedOp{
			{"t1", "x\n", "x\ncall('a')\n", 1},
		}, "x\ncall('b')\n", 2, false, []string{"unknown::uncertain"}},
		{"unrecorded file matching baseline", map[string]string{"f.go": "a\nb\n"}, nil, "a\nb\nc\n", 2, false, []string{"baseline::exact"}},
		{"unrecorded line absent from baseline", map[string]string{"f.go": "a\nb\n"}, nil, "a\nb\nc\n", 3, false, []string{"unknown::uncertain"}},
		{"unrecorded file absent from baseline", map[string]string{"g.go": "a\n"}, nil, "a\n", 1, false, []string{"unknown::uncertain"}},
		{"no baseline snapshot keeps legacy answer", nil, nil, "a\n", 1, false, []string{"baseline::exact"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, bs := historyFixture(t, tc.baseline, tc.ops)
			w := newWalker(db, bs)
			verdicts, err := w.walk("f.go", tc.current, []int{tc.line}, 0, tc.all)
			if err != nil {
				t.Fatal(err)
			}
			reported := verdicts[0].trail
			if !tc.all {
				reported = nil
			}
			reported = append(reported, verdicts[0])
			var got []string
			for _, v := range reported {
				got = append(got, v.kind+":"+v.turnID+":"+string(v.confidence))
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("walk = %v; want %v", got, tc.want)
			}
		})
	}
}
