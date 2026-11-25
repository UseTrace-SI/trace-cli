package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionMarkdownGolden(t *testing.T) {
	t.Setenv("TRACE_HOME", t.TempDir())
	root, db, _ := fixture(t)
	for _, q := range []string{
		"UPDATE turns SET prompt='deploy with AKIAIOSFODNN7EXAMPLE now',started_at=1769178600000,ended_at=1769178660000 WHERE id='t1'",
		"UPDATE sessions SET started_at=1769178600000,ended_at=1769178700000 WHERE id='s'",
		"INSERT INTO keyframes(id,turn_id,seq,manifest_hash,tag,created_at) VALUES('k','t1',3,'h','turn',2)",
		"INSERT INTO messages(id,session_id,turn_id,role,content,at) VALUES('m1','s','t1','assistant','early draft',5),('m2','s','t1','assistant','Done: edited f.txt',6),('m3','s','t1','user','hi',7)",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	got, err := SessionMarkdown(db, root, "s")
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"AKIAIOSFODNN7EXAMPLE", ".env", "secret/key.txt"} {
		if strings.Contains(got, leak) {
			t.Fatalf("leaked %q:\n%s", leak, got)
		}
	}
	// The redaction marker's digits depend on the per-install key.
	got = redactedMarker.ReplaceAllString(got, "⟨redacted:$1⟩")
	golden := filepath.Join("testdata", "session.md")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	ids, err := Sessions(db)
	if err != nil || len(ids) != 1 || ids[0] != "s" {
		t.Fatalf("sessions %v %v", ids, err)
	}
}
