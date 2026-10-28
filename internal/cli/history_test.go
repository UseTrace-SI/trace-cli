package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

func TestUnifiedDiffKeepsUnchangedLines(t *testing.T) {
	got := unifiedDiff("file.txt", "before\nshared\n", "after\nshared\n")
	for _, line := range []string{"-before\n", "+after\n", " shared\n"} {
		if !strings.Contains(got, line) {
			t.Fatalf("missing %q in %s", line, got)
		}
	}
	if strings.Contains(got, "-shared") || strings.Contains(got, "+shared") {
		t.Fatalf("unchanged line replaced: %s", got)
	}
}

func BenchmarkLog5000Steps(b *testing.B) {
	root := b.TempDir()
	dir := filepath.Join(root, ".trace")
	db, err := store.Open(filepath.Join(dir, "trace.db"))
	if err != nil {
		b.Fatal(err)
	}
	bs, err := blob.Open(filepath.Join(dir, "objects"))
	if err != nil {
		b.Fatal(err)
	}
	hash, err := (manifest.Manifest{}).Save(bs)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for step := 0; step < 5000; step++ {
		if _, err := tx.Exec("INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) VALUES(?,?,?,?,?)", fmt.Sprintf("k%05d", step), step, hash, "turn", step); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if err := db.Close(); err != nil {
		b.Fatal(err)
	}
	jsonOutput := true
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := logCommand(&root, &jsonOutput)
		cmd.SetOut(io.Discard)
		if err := cmd.Execute(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWhy5000UnrelatedChanges(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("keep\nA\n"), 0600); err != nil {
		b.Fatal(err)
	}
	dir := filepath.Join(root, ".trace")
	db, err := store.Open(filepath.Join(dir, "trace.db"))
	if err != nil {
		b.Fatal(err)
	}
	bs, err := blob.Open(filepath.Join(dir, "objects"))
	if err != nil {
		b.Fatal(err)
	}
	hA, err := bs.PutBytes([]byte("keep\nA\n"))
	if err != nil {
		b.Fatal(err)
	}
	hB, err := bs.PutBytes([]byte("keep\nB\n"))
	if err != nil {
		b.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for step := 1; step <= 5000; step++ {
		before, after := hA, hB
		if step%2 == 0 {
			before, after = hB, hA
		}
		if _, err := tx.Exec("INSERT INTO ops(id,path,kind,start_line,end_line,before_hash,after_hash,source,at) VALUES(?,?,?,?,?,?,?,?,?)", fmt.Sprintf("o%05d", step), "file.txt", "modify", 2, 2, before, after, "watcher", step); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if err := db.Close(); err != nil {
		b.Fatal(err)
	}
	jsonOutput := true
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := whyCommand(&root, &jsonOutput)
		cmd.SetArgs([]string{"file.txt:1"})
		cmd.SetOut(io.Discard)
		if err := cmd.Execute(); err != nil {
			b.Fatal(err)
		}
	}
}
