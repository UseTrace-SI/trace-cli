package cli

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/gitx"
)

// cloneRewriteRepo clones from into a new checkout and initializes Trace
// there, as a teammate or a recovered machine would.
func cloneRewriteRepo(t *testing.T, from *rewriteRepo) *rewriteRepo {
	t.Helper()
	r := &rewriteRepo{t: t, root: filepath.Join(t.TempDir(), "clone")}
	// --no-local transfers only reachable objects, as a clone from a server
	// does; a local clone would hardlink the whole object store.
	if out, err := exec.Command("git", "clone", "-q", "--no-local", from.root, r.root).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v: %s", err, out)
	}
	r.initTrace()
	return r
}

// Attribution recorded in one checkout must survive in another checkout of the
// same repository after its history is imported, even though the commits
// that were rewritten exist only in the first checkout.
func TestWhySurvivesRewritesAcrossClones(t *testing.T) {
	a := newRewriteRepo(t, map[string]string{"app.go": baseGo})
	base := a.git("rev-parse", "HEAD")
	a.agentTurn("first", map[string]string{"app.go": baseGo + "\nfunc First() {}\n"})
	a.git("commit", "-q", "-a", "-m", "first")
	first := a.git("rev-parse", "HEAD")
	a.agentTurn("second", map[string]string{"app.go": baseGo + "\nfunc First() {}\n\nfunc Second() {}\n"})
	a.git("commit", "-q", "-a", "-m", "second")
	second := a.git("rev-parse", "HEAD")
	a.sequence(base, "pick "+first, "squash "+second)
	a.git("commit", "-q", "--amend", "-m", "first and second")
	checks := []lineCheck{
		{"app.go", "func First() {}", "codex", "first", "exact", []string{"squash", "amend"}},
		{"app.go", "func Second() {}", "codex", "second", "exact", []string{"squash", "amend"}},
		{"app.go", "\treturn 1", "baseline", "", "exact", nil},
	}
	a.check(checks)
	bundle := filepath.Join(t.TempDir(), "history.tar.gz")
	if err := writeBundle(a.root, bundle); err != nil {
		t.Fatal(err)
	}
	b := cloneRewriteRepo(t, a)
	for _, gone := range []string{first, second} {
		if gitx.CommitExists(b.root, gone) {
			t.Fatalf("rewritten commit %s unexpectedly exists in the clone", gone)
		}
	}
	if err := readBundle(b.root, bundle); err != nil {
		t.Fatal(err)
	}
	b.check(checks)
	var rewrites int
	if err := b.e.DB.QueryRow("SELECT COUNT(*) FROM commit_rewrites").Scan(&rewrites); err != nil || rewrites == 0 {
		t.Fatalf("post-rewrite pairs were not imported: %d, %v", rewrites, err)
	}
	// Importing the same bundle again is a no-op.
	if err := readBundle(b.root, bundle); err != nil {
		t.Fatal(err)
	}
	b.check(checks)
}
