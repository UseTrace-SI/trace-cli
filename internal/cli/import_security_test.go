package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/event"
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

// sinceExcludedHistory returns a repository whose step 1 was recorded before
// .npmrc was excluded: its snapshot and an operation hold the file's bytes.
func sinceExcludedHistory(t *testing.T) (root string, secret blob.Hash) {
	t.Helper()
	root = competitiveRepo(t)
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.Blobs.PutBytes([]byte("package app\n"))
	if err != nil {
		t.Fatal(err)
	}
	secret, err = e.Blobs.PutBytes([]byte("//registry.example/:_authToken=npm_old0123456789\n"))
	if err != nil {
		t.Fatal(err)
	}
	mh, err := (manifest.Manifest{"app.go": {Hash: app, Mode: 0644}, ".npmrc": {Hash: secret, Mode: 0644}}).Save(e.Blobs)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex',?,1)", []any{root}},
		{"INSERT INTO turns(id,session_id,seq,prompt,started_at,ended_at) VALUES('t','s',1,'add the app',1,2)", nil},
		{"INSERT INTO keyframes(id,turn_id,seq,manifest_hash,tag,created_at) VALUES('k-legacy','t',1,?,'turn',2)", []any{mh}},
		{"INSERT INTO ops(id,turn_id,session_id,path,kind,start_line,end_line,after_hash,source,at) VALUES('o-app','t','s','app.go','add',1,1,?,'hook',2)", []any{app}},
		{"INSERT INTO ops(id,turn_id,session_id,path,kind,start_line,end_line,after_hash,source,at) VALUES('o-npmrc','t','s','.npmrc','add',1,1,?,'hook',2)", []any{secret}},
	} {
		if _, err := e.DB.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.query, err)
		}
	}
	// A later turn attributes a queued watcher change to .npmrc, recorded by an
	// earlier version: the turn end snapshots the whole operation row as a
	// sync correction, hashes included.
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package app\n"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	ev := event.Event{Agent: "codex", AgentSessionID: "s2", Worktree: root, At: now, Kind: event.TurnStart, Prompt: "tidy up"}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO ops(id,path,kind,before_hash,after_hash,source,at) VALUES('o-watch','.npmrc','modify',?,?,'watcher',?)", secret, secret, now+1); err != nil {
		t.Fatal(err)
	}
	ev.Kind, ev.At = event.TurnEnd, now+2
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	var correction string
	if err := e.DB.QueryRow("SELECT data FROM sync_updates WHERE table_name='ops' AND row_id='o-watch'").Scan(&correction); err != nil || !strings.Contains(correction, string(secret)) {
		t.Fatalf("turn end did not record the operation correction with its hashes: %q %v", correction, err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return root, secret
}

func objectCount(t *testing.T, root string) int {
	t.Helper()
	n := 0
	if err := filepath.WalkDir(filepath.Join(root, ".trace", "objects"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func bundleEntries(t *testing.T, archive string) map[string]bool {
	t.Helper()
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
	names := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names[h.Name] = true
	}
}

// History recorded before a secret exclusion was added can list the file
// (here a committed .npmrc with a token). By default export and handoff
// leave its name and bytes out of the bundle, which still imports, rewinds
// and explains lines. --include-excluded-history keeps them, with a warning.
func TestBundleLeavesOutSinceExcludedHistory(t *testing.T) {
	source, secret := sinceExcludedHistory(t)
	secretEntry := ".trace/objects/" + string(secret[:2]) + "/" + string(secret[2:])
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	jsonOutput := false
	objectsBefore := objectCount(t, source)
	export := exportCommand(&source, &jsonOutput)
	export.SetArgs([]string{"--format", "bundle", "--out", archive})
	var exportOut bytes.Buffer
	export.SetOut(&exportOut)
	if err := export.Execute(); err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(exportOut.String(), "Left out earlier snapshots of 1 excluded path(s): .npmrc") {
		t.Fatalf("export did not report the stripped path: %q", exportOut.String())
	}
	if bundleEntries(t, archive)[secretEntry] {
		t.Fatal("bundle carries the .npmrc bytes")
	}
	if after := objectCount(t, source); after != objectsBefore {
		t.Fatalf("export wrote %d objects into the live store", after-objectsBefore)
	}
	handoffOut := filepath.Join(t.TempDir(), "handoff.tar.gz")
	handoff := handoffCommand(&source, &jsonOutput)
	handoff.SetArgs([]string{"--out", handoffOut})
	var handoffText bytes.Buffer
	handoff.SetOut(&handoffText)
	if err := handoff.Execute(); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if bundleEntries(t, handoffOut)[secretEntry] || !strings.Contains(handoffText.String(), ".npmrc") {
		t.Fatalf("handoff carried .npmrc or did not report it: %q", handoffText.String())
	}
	// The local history is unchanged.
	local, err := engine.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := local.ManifestAt(1); err != nil || m[".npmrc"].Hash != secret {
		t.Fatalf("export changed local history: %v %v", m, err)
	}
	local.Close()

	target := competitiveRepo(t)
	if err := readBundle(target, archive); err != nil {
		t.Fatalf("import of a stripped bundle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, ".npmrc"), []byte("//registry.example/:_authToken=local\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := engine.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	m, err := got.ManifestAt(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m[".npmrc"]; ok || m["app.go"].Hash == "" {
		t.Fatalf("imported snapshot: %v", m)
	}
	var before, after sql.NullString
	if err := got.DB.QueryRow("SELECT before_hash,after_hash FROM ops WHERE id='o-npmrc'").Scan(&before, &after); err != nil || before.Valid || after.Valid {
		t.Fatalf("imported .npmrc operation still references content: %v %v %v", before, after, err)
	}
	if _, err := got.Blobs.Get(secret); err == nil {
		t.Fatal("import stored the .npmrc bytes")
	}
	var correction string
	if err := got.DB.QueryRow("SELECT data FROM sync_updates WHERE table_name='ops' AND row_id='o-watch'").Scan(&correction); err != nil || strings.Contains(correction, string(secret)) || !strings.Contains(correction, `"after_hash":null`) {
		t.Fatalf("imported operation correction still references .npmrc content: %q %v", correction, err)
	}
	if _, _, err := got.Rewind(1, "", false); err != nil {
		t.Fatalf("rewind to imported step: %v", err)
	}
	if _, err := got.GC(0, true); err != nil {
		t.Fatalf("GC over imported history: %v", err)
	}
	got.Close()
	if b, err := os.ReadFile(filepath.Join(target, "app.go")); err != nil || string(b) != "package app\n" {
		t.Fatalf("app.go not restored: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(target, ".npmrc")); err != nil || string(b) != "//registry.example/:_authToken=local\n" {
		t.Fatalf("rewind touched excluded .npmrc: %q %v", b, err)
	}
	whyJSON := true
	why := whyCommand(&target, &whyJSON)
	why.SetArgs([]string{"app.go:1"})
	var whyOut bytes.Buffer
	why.SetOut(&whyOut)
	if err := why.Execute(); err != nil {
		t.Fatalf("why on imported history: %v", err)
	}
	var lines []whyResult
	if err := json.Unmarshal(whyOut.Bytes(), &lines); err != nil || len(lines) != 1 || lines[0].Step != 1 {
		t.Fatalf("why app.go:1 = %s (%v)", whyOut.String(), err)
	}

	// Opting in keeps the bytes and warns.
	kept := filepath.Join(t.TempDir(), "kept.tar.gz")
	include := exportCommand(&source, &jsonOutput)
	include.SetArgs([]string{"--format", "bundle", "--out", kept, "--include-excluded-history"})
	var stderr bytes.Buffer
	include.SetOut(io.Discard)
	include.SetErr(&stderr)
	if err := include.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bundleEntries(t, kept)[secretEntry] || !strings.Contains(stderr.String(), "warning") || !strings.Contains(stderr.String(), ".npmrc") {
		t.Fatalf("opt-in bundle: secret kept %v, warning %q", bundleEntries(t, kept)[secretEntry], stderr.String())
	}
	keptTarget := competitiveRepo(t)
	if err := readBundle(keptTarget, kept); err != nil {
		t.Fatalf("import of a bundle keeping .npmrc: %v", err)
	}
	// A direct database import (a local copy, not an export) is unchanged.
	if _, err := importDatabase(context.Background(), competitiveRepo(t), filepath.Join(source, ".trace", "trace.db")); err != nil {
		t.Fatalf("database import of history listing .npmrc failed: %v", err)
	}
}

// Exports leave since-excluded content out, so a history and its own export
// differ only there. Re-importing either way must report duplicates and keep
// the local rows, not a conflict.
func TestReimportingStrippedHistoryKeepsLocalRows(t *testing.T) {
	a, secret := sinceExcludedHistory(t)
	stripped := filepath.Join(t.TempDir(), "stripped.tar.gz")
	if _, err := exportBundle(a, stripped, bundleOptions{}); err != nil {
		t.Fatal(err)
	}
	report, err := readBundleReport(context.Background(), a, stripped)
	if err != nil {
		t.Fatalf("re-importing A's own export into A: %v", err)
	}
	if report.Imported != 0 || report.Skipped == 0 {
		t.Fatalf("self re-import report %+v; want only duplicates", report)
	}
	e, err := engine.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	m, err := e.ManifestAt(1)
	var after sql.NullString
	if err == nil {
		err = e.DB.QueryRow("SELECT after_hash FROM ops WHERE id='o-npmrc'").Scan(&after)
	}
	e.Close()
	if err != nil || m[".npmrc"].Hash != secret || after.String != string(secret) {
		t.Fatalf("self re-import changed local rows: %v %v %v", m, after, err)
	}

	// A -> B -> A through handoff bundles.
	b := competitiveRepo(t)
	if err := readBundle(b, stripped); err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(t.TempDir(), "back.tar.gz")
	jsonOutput := false
	handoff := handoffCommand(&b, &jsonOutput)
	handoff.SetArgs([]string{"--out", back})
	handoff.SetOut(io.Discard)
	if err := handoff.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := readBundleReport(context.Background(), a, back); err != nil {
		t.Fatalf("importing B's handoff back into A: %v", err)
	}

	// The reverse: A's opt-in export, which keeps .npmrc, into B's stripped
	// copy.
	full := filepath.Join(t.TempDir(), "full.tar.gz")
	if _, err := exportBundle(a, full, bundleOptions{IncludeExcludedHistory: true}); err != nil {
		t.Fatal(err)
	}
	report, err = readBundleReport(context.Background(), b, full)
	if err != nil {
		t.Fatalf("importing an unstripped export into a stripped copy: %v", err)
	}
	if report.Imported != 0 {
		t.Fatalf("reverse import added rows: %+v", report)
	}
	e, err = engine.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if m, err := e.ManifestAt(1); err != nil || m[".npmrc"].Hash != "" {
		t.Fatalf("reverse import replaced the stripped local row: %v %v", m, err)
	}
	// The kept rows' excluded content was not copied in unreferenced.
	var unstripped string
	if err := e.DB.QueryRow("SELECT manifest_hash FROM keyframes WHERE seq=1").Scan(&unstripped); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Blobs.Get(secret); err == nil {
		t.Fatal("reverse import copied the .npmrc bytes into B's object store")
	}
	if source, err := engine.Open(a); err == nil {
		var original string
		err = source.DB.QueryRow("SELECT manifest_hash FROM keyframes WHERE seq=1").Scan(&original)
		source.Close()
		if err != nil || original == unstripped {
			t.Fatalf("A's manifest %q, B's %q: %v", original, unstripped, err)
		}
		if _, err := e.Blobs.Get(blob.Hash(original)); err == nil {
			t.Fatal("reverse import copied the unstripped manifest into B's object store")
		}
	} else {
		t.Fatal(err)
	}

	// A genuinely different row still conflicts.
	if _, err := e.DB.Exec("UPDATE ops SET kind='delete' WHERE id='o-npmrc'"); err != nil {
		t.Fatal(err)
	}
	if _, err := readBundleReport(context.Background(), b, full); err == nil || !strings.Contains(err.Error(), "conflicting ops row") {
		t.Fatalf("changed operation row was not a conflict: %v", err)
	}
}

func TestHandoffJSONListsLeftOutPaths(t *testing.T) {
	source, _ := sinceExcludedHistory(t)
	jsonOutput := true
	out := filepath.Join(t.TempDir(), "handoff.tar.gz")
	cmd := handoffCommand(&source, &jsonOutput)
	cmd.SetArgs([]string{"--out", out})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Path     string   `json:"path"`
		Context  string   `json:"context"`
		LeftOut  []string `json:"historical_paths_now_excluded"`
		Included bool     `json:"excluded_history_included"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("%v: %q", err, stdout.String())
	}
	if got.Path != out || got.Context == "" || len(got.LeftOut) != 1 || got.LeftOut[0] != ".npmrc" || got.Included {
		t.Fatalf("handoff --json = %+v", got)
	}
}
