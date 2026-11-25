package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

const maxBundleBytes int64 = 4 << 30
const maxBundleEntries = 100000

// bundleOptions selects what a bundle carries beyond the default.
type bundleOptions struct {
	// IncludeExcludedHistory keeps snapshots of paths Trace now excludes, such
	// as credential files captured before their exclusion was added.
	IncludeExcludedHistory bool
}

func writeBundle(root, out string) error {
	_, err := exportBundle(root, out, bundleOptions{})
	return err
}

// exportBundle writes a bundle and returns the since-excluded paths its
// history listed: left out by default, kept with IncludeExcludedHistory.
func exportBundle(root, out string, opts bundleOptions) ([]string, error) {
	var found []string
	err := buildBundle(root, out, opts, &found)
	return found, err
}

// A portable bundle contains a consistent history snapshot and immutable
// objects only. Credentials, logs, hooks and companion databases stay local,
// and by default so do snapshots of paths Trace now excludes: the bundle's
// copy of the history is rewritten so neither their names nor their bytes
// are exported.
func buildBundle(root, out string, opts bundleOptions, found *[]string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	stage, err := os.MkdirTemp(filepath.Join(root, ".trace"), ".export-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	snapshot := filepath.Join(stage, "trace.db")
	if err := snapshotHistory(ctx, filepath.Join(root, ".trace", "trace.db"), snapshot); err != nil {
		return fmt.Errorf("snapshotting history: %w", err)
	}
	liveDir := filepath.Join(root, ".trace", "objects")
	bs, err := blob.OpenReadOnly(liveDir)
	if err != nil {
		return err
	}
	// Stripped manifests are new objects private to this export. They live in
	// the stage, where GC of the live store cannot remove them mid-export.
	stageDir := filepath.Join(stage, "objects")
	staged, err := blob.Open(stageDir)
	if err != nil {
		return err
	}
	if *found, err = stripBundleHistory(ctx, snapshot, bs, staged, !opts.IncludeExcludedHistory); err != nil {
		return fmt.Errorf("leaving excluded paths out of the bundle: %w", err)
	}
	db, err := openHistoryReadOnly(snapshot)
	if err != nil {
		return err
	}
	defer db.Close()
	hashes, err := historyHashes(db, staged, bs)
	if err != nil {
		return err
	}
	files := map[string]string{".trace/trace.db": snapshot}
	for _, h := range hashes {
		store, dir := bs, liveDir
		if _, err := os.Lstat(filepath.Join(stageDir, string(h[:2]), string(h[2:]))); err == nil {
			store, dir = staged, stageDir
		}
		// Verify every byte before publishing a supposedly recoverable archive.
		reader, err := store.Get(h)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(io.Discard, reader)
		if err := errors.Join(copyErr, reader.Close()); err != nil {
			return err
		}
		files[".trace/objects/"+string(h[:2])+"/"+string(h[2:])] = filepath.Join(dir, string(h[:2]), string(h[2:]))
		chunks, err := store.Chunks(h)
		if err != nil {
			return err
		}
		for _, ch := range chunks {
			files[".trace/objects/chunks/"+string(ch[:2])+"/"+string(ch[2:])] = filepath.Join(dir, "chunks", string(ch[:2]), string(ch[2:]))
		}
	}
	if len(files) > maxBundleEntries {
		return fmt.Errorf("bundle exceeds %d entries", maxBundleEntries)
	}
	f, err := os.CreateTemp(filepath.Dir(out), ".trace-bundle-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var size int64
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			tw.Close()
			gz.Close()
			f.Close()
			return err
		}
		info, err := os.Lstat(files[name])
		if err != nil || !info.Mode().IsRegular() {
			tw.Close()
			gz.Close()
			f.Close()
			return fmt.Errorf("bundle source is missing or not a regular file: %s", name)
		}
		size += info.Size()
		if size > maxBundleBytes {
			tw.Close()
			gz.Close()
			f.Close()
			return fmt.Errorf("bundle exceeds 4 GiB")
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
			tw.Close()
			gz.Close()
			f.Close()
			return err
		}
		src, err := os.Open(files[name])
		if err != nil {
			tw.Close()
			gz.Close()
			f.Close()
			return err
		}
		_, copyErr := io.Copy(tw, src)
		err = errors.Join(copyErr, src.Close())
		if err != nil {
			tw.Close()
			gz.Close()
			f.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		f.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), out)
}

// stripBundleHistory finds the since-excluded paths a history snapshot still
// lists, in keyframe manifests or in operations that stored their bytes.
// With apply it rewrites the snapshot: keyframes point at manifests without
// those paths, and such operations keep their metadata but no longer
// reference content. The history stays self-consistent, so the bundle still
// imports, rewinds and explains lines; restores skip these paths anyway.
func stripBundleHistory(ctx context.Context, snapshot string, bs, staged *blob.Store, apply bool) ([]string, error) {
	abs, err := filepath.Abs(snapshot)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: abs}).String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	found := map[string]bool{}
	manifests, err := queryStrings(ctx, tx, "SELECT DISTINCT manifest_hash FROM keyframes")
	if err != nil {
		return nil, err
	}
	for _, h := range manifests {
		if !apply {
			m, err := manifest.Load(blob.Hash(h), bs)
			if err != nil {
				return nil, err
			}
			for rel := range m {
				if manifest.Excluded(rel) {
					found[rel] = true
				}
			}
			continue
		}
		stripped, dropped, err := manifest.StripExcluded(blob.Hash(h), bs, staged)
		if err != nil {
			return nil, err
		}
		for _, rel := range dropped {
			found[rel] = true
		}
		if stripped != blob.Hash(h) {
			if _, err := tx.ExecContext(ctx, "UPDATE keyframes SET manifest_hash=? WHERE manifest_hash=?", string(stripped), h); err != nil {
				return nil, err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,path FROM ops WHERE COALESCE(before_hash,'')!='' OR COALESCE(after_hash,'')!=''")
	if err != nil {
		return nil, err
	}
	var ops []string
	for rows.Next() {
		var id, rel string
		if err := rows.Scan(&id, &rel); err != nil {
			rows.Close()
			return nil, err
		}
		if manifest.Excluded(rel) {
			found[rel] = true
			ops = append(ops, id)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if apply {
		for _, id := range ops {
			if _, err := tx.ExecContext(ctx, "UPDATE ops SET before_hash=NULL, after_hash=NULL WHERE id=?", id); err != nil {
				return nil, err
			}
		}
	}
	// Corrections recorded for operations snapshot the whole row, hashes
	// included.
	updates, err := tx.QueryContext(ctx, "SELECT id,data FROM sync_updates WHERE table_name='ops'")
	if err != nil {
		return nil, err
	}
	stripped := map[string]string{}
	for updates.Next() {
		var id, data string
		if err := updates.Scan(&id, &data); err != nil {
			updates.Close()
			return nil, err
		}
		decoder := json.NewDecoder(strings.NewReader(data))
		decoder.UseNumber()
		var op map[string]any
		if err := decoder.Decode(&op); err != nil {
			updates.Close()
			return nil, fmt.Errorf("decoding operation correction %s: %w", id, err)
		}
		if !manifest.StripOpContent(op) {
			continue
		}
		rel, _ := op["path"].(string)
		found[rel] = true
		b, err := json.Marshal(op)
		if err != nil {
			updates.Close()
			return nil, err
		}
		stripped[id] = string(b)
	}
	if err := errors.Join(updates.Err(), updates.Close()); err != nil {
		return nil, err
	}
	if apply {
		for id, data := range stripped {
			if _, err := tx.ExecContext(ctx, "UPDATE sync_updates SET data=? WHERE id=?", data, id); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(found))
	for rel := range found {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths, nil
}

func queryStrings(ctx context.Context, tx *sql.Tx, query string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, v)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}

func bundlePath(name string) (string, error) {
	if strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || path.Clean(name) != strings.TrimSuffix(name, "/") || !strings.HasPrefix(name, ".trace/") {
		return "", fmt.Errorf("unsafe bundle path")
	}
	rel := strings.TrimPrefix(strings.TrimSuffix(name, "/"), ".trace/")
	if rel == "" || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("unsafe bundle path")
	}
	return rel, nil
}

func portableBundleFile(rel string) bool {
	if rel == "trace.db" {
		return true
	}
	parts := strings.Split(rel, "/")
	if len(parts) == 4 && parts[0] == "objects" && parts[1] == "chunks" {
		parts = append(parts[:1], parts[2:]...)
	}
	if len(parts) != 3 || parts[0] != "objects" || len(parts[1]) != 2 || len(parts[2]) != 62 {
		return false
	}
	for _, c := range parts[1] + parts[2] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Extraction never writes into the live history. Invalid archives leave the
// existing database and files intact; successful validation merges transactionally.
func readBundle(root, in string) error {
	_, err := readBundleReport(context.Background(), root, in)
	return err
}
func readBundleReport(parent context.Context, root, in string) (importReport, error) {
	report := importReport{}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	unlock, err := maintenanceLock(root)
	if err != nil {
		return report, err
	}
	defer unlock()
	f, err := os.Open(in)
	if err != nil {
		return report, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return report, err
	}
	defer gz.Close()
	stage, err := os.MkdirTemp(filepath.Join(root, ".trace"), ".import-*")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(stage)
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	var size int64
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return report, err
		}
		if count >= maxBundleEntries {
			return report, fmt.Errorf("bundle has too many entries")
		}
		rel, err := bundlePath(hdr.Name)
		if err != nil {
			return report, err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != 0 && hdr.Typeflag != tar.TypeDir {
			return report, fmt.Errorf("bundle links and special files are forbidden")
		}
		if hdr.Size < 0 || hdr.Size > maxBundleBytes-size {
			return report, fmt.Errorf("bundle exceeds 4 GiB")
		}
		size += hdr.Size
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		// Legacy bundles may carry local runtime files. Never import those.
		if !portableBundleFile(rel) {
			continue
		}
		if rel != "trace.db" && hdr.Size > 8<<20 {
			return report, fmt.Errorf("stored object exceeds 8 MiB")
		}
		if seen[rel] {
			return report, fmt.Errorf("duplicate bundle entry")
		}
		seen[rel] = true
		target := filepath.Join(stage, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return report, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return report, err
		}
		_, copyErr := io.Copy(out, tr)
		if err := errors.Join(copyErr, out.Close()); err != nil {
			return report, err
		}
	}
	padding, err := io.ReadAll(io.LimitReader(gz, 1<<20+1))
	if err != nil {
		return report, fmt.Errorf("invalid compressed bundle: %w", err)
	}
	if len(padding) > 1<<20 {
		return report, fmt.Errorf("excessive bundle padding")
	}
	for _, value := range padding {
		if value != 0 {
			return report, fmt.Errorf("unexpected data after bundle")
		}
	}
	if !seen["trace.db"] {
		return report, fmt.Errorf("bundle is missing trace.db")
	}
	return mergeHistory(ctx, root, filepath.Join(stage, "trace.db"))
}
