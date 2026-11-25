package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
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

func bundleWithSnapshotPath(t *testing.T, rel string) string {
	t.Helper()
	source := competitiveRepo(t)
	e, err := engine.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	h, err := e.Blobs.PutBytes([]byte("#!/bin/sh\necho pwned > PWNED\n"))
	if err != nil {
		t.Fatal(err)
	}
	mh, err := (manifest.Manifest{rel: {Hash: h, Mode: 0755}}).Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) VALUES('k-shared',1,?,'turn',2)", mh); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	// Build the archive by hand, as a crafted bundle would be: the exporter
	// itself refuses to publish these paths.
	snapshot := filepath.Join(t.TempDir(), "trace.db")
	if err := snapshotHistory(context.Background(), filepath.Join(source, ".trace", "trace.db"), snapshot); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{".trace/trace.db": snapshot}
	objects := filepath.Join(source, ".trace", "objects")
	if err := filepath.WalkDir(objects, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(objects, p)
		files[".trace/objects/"+filepath.ToSlash(rel)] = p
		return err
	}); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "shared.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, src := range files {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
	return archive
}

// A shared bundle's snapshot is restored by a later rewind. Paths into Git
// metadata, at any depth or in any letter case, would install hooks that run
// on the next commit, so import must reject them.
func TestBundleRejectsSnapshotPathsIntoGitMetadata(t *testing.T) {
	for _, rel := range []string{"sub/.git/hooks/pre-commit", ".GIT/hooks/pre-commit", ".Git/config", ".TRACE/trace.db", ".MCP.json"} {
		t.Run(rel, func(t *testing.T) {
			archive := bundleWithSnapshotPath(t, rel)
			target := competitiveRepo(t)
			if out, err := exec.Command("git", "init", "-q", filepath.Join(target, "sub")).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			if err := readBundle(target, archive); err == nil {
				e, err := engine.Open(target)
				if err != nil {
					t.Fatal(err)
				}
				_, _, rewindErr := e.Rewind(1, "", false)
				e.Close()
				t.Fatalf("import accepted protected snapshot path %q (rewind error: %v)", rel, rewindErr)
			}
			if _, err := os.Stat(filepath.Join(target, "sub", ".git", "hooks", "pre-commit")); err == nil {
				t.Fatal("hook installed into nested repository")
			}
		})
	}
}

// History recorded before a secret exclusion was added can list the file
// (here a committed .npmrc). Export, handoff bundles and import must still
// work; a later rewind restores everything else and leaves the file alone.
func TestBundleCarriesHistoryListingSinceExcludedFiles(t *testing.T) {
	source := competitiveRepo(t)
	e, err := engine.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.Blobs.PutBytes([]byte("package app\n"))
	if err != nil {
		t.Fatal(err)
	}
	npmrc, err := e.Blobs.PutBytes([]byte("registry=https://registry.example/\n"))
	if err != nil {
		t.Fatal(err)
	}
	mh, err := (manifest.Manifest{"app.go": {Hash: app, Mode: 0644}, ".npmrc": {Hash: npmrc, Mode: 0644}}).Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO keyframes(id,seq,manifest_hash,tag,created_at) VALUES('k-legacy',1,?,'turn',2)", mh); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := writeBundle(source, archive); err != nil {
		t.Fatalf("export of history listing .npmrc failed: %v", err)
	}
	jsonOutput := false
	handoff := handoffCommand(&source, &jsonOutput)
	handoff.SetArgs([]string{"--out", filepath.Join(t.TempDir(), "handoff.tar.gz")})
	handoff.SetOut(io.Discard)
	if err := handoff.Execute(); err != nil {
		t.Fatalf("handoff of history listing .npmrc failed: %v", err)
	}
	target := competitiveRepo(t)
	if err := readBundle(target, archive); err != nil {
		t.Fatalf("import of history listing .npmrc failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, ".npmrc"), []byte("//registry.example/:_authToken=local\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := engine.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if _, _, err := got.Rewind(1, "", false); err != nil {
		t.Fatalf("rewind to imported step: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(target, "app.go")); err != nil || string(b) != "package app\n" {
		t.Fatalf("app.go not restored: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(target, ".npmrc")); err != nil || string(b) != "//registry.example/:_authToken=local\n" {
		t.Fatalf("rewind touched excluded .npmrc: %q %v", b, err)
	}
	dbTarget := competitiveRepo(t)
	if _, err := importDatabase(context.Background(), dbTarget, filepath.Join(source, ".trace", "trace.db")); err != nil {
		t.Fatalf("database import of history listing .npmrc failed: %v", err)
	}
}
