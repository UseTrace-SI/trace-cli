package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
)

func TestBundleRejectsUnsafeArchivesWithoutChangingHistory(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind byte
		size int64
	}{
		{".trace/../../escaped", tar.TypeReg, 1}, {".trace/../escaped", tar.TypeReg, 1},
		{".trace/link", tar.TypeSymlink, 0}, {".trace/link", tar.TypeLink, 0},
		{".trace/objects\\escape", tar.TypeReg, 1}, {"/tmp/escaped", tar.TypeReg, 1},
		{".trace/trace.db", tar.TypeReg, maxBundleBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := competitiveRepo(t)
			archive := filepath.Join(t.TempDir(), "bad.tar.gz")
			f, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tar.Header{Name: tc.name, Typeflag: tc.kind, Size: tc.size, Linkname: "../../escaped", Mode: 0600}); err != nil {
				t.Fatal(err)
			}
			if tc.size == 1 {
				if _, err := tw.Write([]byte("x")); err != nil {
					t.Fatal(err)
				}
			}
			_ = tw.Close()
			_ = gz.Close()
			_ = f.Close()
			if err := readBundle(root, archive); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			e, err := engine.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			var n int
			if err := e.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n); err != nil || n != 0 {
				t.Fatalf("history changed: %d %v", n, err)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escaped")); !os.IsNotExist(err) {
				t.Fatal("wrote outside history")
			}
		})
	}
}

func TestBundleSnapshotRoundTripAndPrivateFiles(t *testing.T) {
	source := competitiveRepo(t)
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("exact bytes\x00\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := engine.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	// Keep the writer open: the export must include committed WAL content.
	if _, err := e.DB.Exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex',?,1)", source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".trace", "login.json"), []byte("private connection"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := writeBundle(source, archive); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(archive)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("bundle permissions: %v %v", info, err)
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !portableBundleFile(strings.TrimPrefix(h.Name, ".trace/")) {
			t.Fatalf("private runtime file exported: %s", h.Name)
		}
	}
	target := competitiveRepo(t)
	if err := readBundle(target, archive); err != nil {
		t.Fatal(err)
	}
	got, err := engine.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	var n int
	if err := got.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE id='s'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("WAL session lost: %d %v", n, err)
	}
	m, err := got.ManifestAt(0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := got.Blobs.Get(m["file.txt"].Hash)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil || string(b) != "exact bytes\x00\n" {
		t.Fatalf("file bytes changed: %q %v", b, err)
	}
	report, err := importDatabase(context.Background(), target, filepath.Join(source, ".trace", "trace.db"))
	if err != nil || report.Imported != 0 || report.Skipped < 2 {
		t.Fatalf("duplicate import: %+v %v", report, err)
	}
}

func TestDatabaseImportRejectsSchemaErrorsAndRollsBack(t *testing.T) {
	root := competitiveRepo(t)
	source := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE sessions(id TEXT,agent TEXT); INSERT INTO sessions VALUES('s','codex')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := importDatabase(context.Background(), root, source); err == nil {
		t.Fatal("schema failure reported success")
	}
	sourceRoot := competitiveRepo(t)
	e, err := engine.Open(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('new','codex','repo',1); PRAGMA foreign_keys=OFF; INSERT INTO turns(id,session_id,seq,started_at) VALUES('bad','missing',1,1)"); err != nil {
		t.Fatal(err)
	}
	e.Close()
	if _, err := importDatabase(context.Background(), root, filepath.Join(sourceRoot, ".trace", "trace.db")); err == nil {
		t.Fatal("broken reference imported")
	}
	target, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var n int
	if err := target.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial import committed: %d %v", n, err)
	}
}

func TestImportRefusesRunningDaemon(t *testing.T) {
	root := competitiveRepo(t)
	f, err := os.OpenFile(filepath.Join(root, ".trace", "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if _, err := importDatabase(context.Background(), root, "missing"); err == nil || !strings.Contains(err.Error(), "stop the Trace daemon") {
		t.Fatalf("active history not protected: %v", err)
	}
}

func TestBundleRejectsTruncatedCompression(t *testing.T) {
	source := competitiveRepo(t)
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := writeBundle(source, archive); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(archive, info.Size()-4); err != nil {
		t.Fatal(err)
	}
	target := competitiveRepo(t)
	if err := readBundle(target, archive); err == nil {
		t.Fatal("truncated gzip accepted")
	}
}
