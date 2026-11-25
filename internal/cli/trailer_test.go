package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/gitx"
)

func (r *rewriteRepo) lastTurn() string {
	r.t.Helper()
	var id string
	if err := r.e.DB.QueryRow("SELECT id FROM turns ORDER BY seq DESC LIMIT 1").Scan(&id); err != nil {
		r.t.Fatal(err)
	}
	return id
}

func TestCommitTrailerOffLeavesMessage(t *testing.T) {
	r := newRewriteRepo(t, map[string]string{"a.txt": "a\n"})
	r.agentTurn("edit", map[string]string{"a.txt": "agent\n"})
	r.git("commit", "-q", "-am", "plain")
	if msg := r.git("log", "-1", "--format=%B"); msg != "plain" {
		t.Fatalf("message changed with trailers off: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(r.root, ".git", "hooks", "prepare-commit-msg")); !os.IsNotExist(err) {
		t.Fatalf("prepare-commit-msg installed by default: %v", err)
	}
}

func TestCommitTrailerOnAddsTurnAndChainsHook(t *testing.T) {
	r := newRewriteRepo(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	hook := filepath.Join(r.root, ".git", "hooks", "prepare-commit-msg")
	user := "#!/usr/bin/env python3\nimport sys\nopen(sys.argv[1], 'a').write('\\nUser-Hook: ran\\n')\n"
	if err := os.WriteFile(hook, []byte(user), 0755); err != nil {
		t.Fatal(err)
	}
	if err := gitx.EnableTrailer(r.root); err != nil {
		t.Fatal(err)
	}
	r.agentTurn("edit a", map[string]string{"a.txt": "agent\n"})
	turn := r.lastTurn()
	r.write("b.txt", "human\n")
	r.git("commit", "-q", "-am", "work")
	msg := r.git("log", "-1", "--format=%B")
	if !strings.Contains(msg, "User-Hook: ran") || strings.Count(msg, gitx.TrailerKey+": "+turn) != 1 {
		t.Fatalf("message: %q", msg)
	}
	// Amending keeps a single trailer for the same turn.
	r.git("commit", "-q", "--amend", "--no-edit")
	if msg := r.git("log", "-1", "--format=%B"); strings.Count(msg, gitx.TrailerKey+": ") != 1 {
		t.Fatalf("amended message: %q", msg)
	}
	// A human-only commit gets no trailer.
	r.write("b.txt", "human again\n")
	r.git("commit", "-q", "-am", "human")
	if msg := r.git("log", "-1", "--format=%B"); strings.Contains(msg, gitx.TrailerKey) {
		t.Fatalf("human commit got a trailer: %q", msg)
	}
	if err := gitx.DisableTrailer(r.root); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(hook); err != nil || string(b) != user {
		t.Fatalf("user hook not restored: %q %v", b, err)
	}
}
