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

// trailerRepo has trailers on and one recorded agent turn that edited a.txt,
// not yet committed.
func trailerRepo(t *testing.T) (*rewriteRepo, string) {
	r := newRewriteRepo(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	if err := gitx.EnableTrailer(r.root); err != nil {
		t.Fatal(err)
	}
	r.agentTurn("edit a", map[string]string{"a.txt": "agent\n"})
	return r, gitx.TrailerKey + ": " + r.lastTurn()
}

func TestCommitTrailerMessageSources(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  string
		want []string
	}{
		{"message file", []string{"-F", "MSG"}, "", []string{"from file"}},
		{"editor", nil, "sed -i 1s/^/edited/", []string{"edited"}},
		{"verbatim with trailers and comments", []string{"--cleanup=verbatim", "-m", "subject\n\nSigned-off-by: Dev <dev@example.invalid>\n# a comment"}, "",
			[]string{"subject", "Signed-off-by: Dev <dev@example.invalid>", "# a comment"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, trailer := trailerRepo(t)
			msg := filepath.Join(t.TempDir(), "msg")
			if err := os.WriteFile(msg, []byte("from file\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.env != "" {
				t.Setenv("GIT_EDITOR", tc.env)
			}
			args := []string{"commit", "-q", "-a"}
			for _, a := range tc.args {
				args = append(args, strings.ReplaceAll(a, "MSG", msg))
			}
			r.git(args...)
			got := r.git("log", "-1", "--format=%B")
			if strings.Count(got, trailer) != 1 {
				t.Fatalf("want one %q in %q", trailer, got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Fatalf("want %q in %q", w, got)
				}
			}
		})
	}
}

func TestCommitTrailerSkipsReusedMessages(t *testing.T) {
	t.Run("amend", func(t *testing.T) {
		r, _ := trailerRepo(t)
		r.git("stash", "-q")
		r.write("b.txt", "human\n")
		r.git("commit", "-q", "-am", "human")
		r.git("stash", "pop", "-q")
		r.git("commit", "-q", "-a", "--amend", "--no-edit")
		if got := r.git("log", "-1", "--format=%B"); got != "human" {
			t.Fatalf("amend changed the message: %q", got)
		}
	})
	t.Run("reuse message", func(t *testing.T) {
		r, _ := trailerRepo(t)
		r.git("commit", "-q", "-a", "-C", "HEAD")
		if got := r.git("log", "-1", "--format=%B"); got != "base" {
			t.Fatalf("-C changed the message: %q", got)
		}
	})
	for _, op := range []string{"cherry-pick", "rebase"} {
		t.Run(op, func(t *testing.T) {
			r := newRewriteRepo(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
			if err := gitx.EnableTrailer(r.root); err != nil {
				t.Fatal(err)
			}
			// A human commit on a side branch touches a.txt; then a recorded
			// turn edits a.txt on main after main's HEAD.
			r.git("checkout", "-q", "-b", "side")
			r.write("a.txt", "a\nside\n")
			r.git("commit", "-q", "-am", "side change")
			r.git("checkout", "-q", "main")
			r.write("b.txt", "main\n")
			r.git("commit", "-q", "-am", "main change")
			r.agentTurn("edit a", map[string]string{"a.txt": "agent\n"})
			r.git("checkout", "-q", "--", "a.txt")
			if op == "cherry-pick" {
				r.git("cherry-pick", "side")
			} else {
				r.git("checkout", "-q", "side")
				r.git("rebase", "-q", "main")
			}
			if got := r.git("log", "-1", "--format=%B"); got != "side change" {
				t.Fatalf("%s changed the message: %q", op, got)
			}
		})
	}
}

func TestConfigSetCommitTrailerReportsManualHook(t *testing.T) {
	r := newRewriteRepo(t, map[string]string{"a.txt": "a\n"})
	r.git("config", "core.hooksPath", ".husky")
	if err := os.MkdirAll(filepath.Join(r.root, ".husky"), 0755); err != nil {
		t.Fatal(err)
	}
	root := r.root
	jsonOutput := false
	cmd := configCommand(&root, &jsonOutput)
	var stdout, stderr strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"set", "commit-trailer", "on"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "commit-trailer on\n" || !strings.Contains(stderr.String(), "Manual step pending") ||
		!strings.Contains(stderr.String(), filepath.Join(".husky", "prepare-commit-msg")) || !strings.Contains(stderr.String(), "trailer-hook") {
		t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(r.root, ".husky", "prepare-commit-msg")); !os.IsNotExist(err) {
		t.Fatalf("wrote a hook inside the worktree: %v", err)
	}
}
