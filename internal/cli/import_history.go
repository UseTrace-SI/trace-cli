package cli

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

type importReport struct {
	Schema   int   `json:"history_schema"`
	Imported int64 `json:"imported"`
	Skipped  int64 `json:"skipped"`
}

func maintenanceLock(root string) (func(), error) {
	info, err := os.Lstat(filepath.Join(root, ".trace"))
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("trace directory must be a real directory")
	}
	f, err := os.OpenFile(filepath.Join(root, ".trace", "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("history is in use; stop the Trace daemon before importing: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

func openHistoryReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA busy_timeout=30000; PRAGMA trusted_schema=OFF"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func importDatabase(ctx context.Context, root, source string) (importReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	unlock, err := maintenanceLock(root)
	if err != nil {
		return importReport{}, err
	}
	defer unlock()
	return mergeHistory(ctx, root, source)
}

// stepColumns maps each imported column holding a step number to the table
// whose seq numbers that step. Every exported history starts at step 0, as
// does the baseline of an initialized checkout, and turn and keyframe steps
// are unique, so an import renumbers rows whose step is taken and rewrites
// the columns referring to those steps. A table must follow the tables whose
// steps it refers to in store.HistoryTables.
var stepColumns = map[string]map[string]string{
	"turns":             {"seq": "turns"},
	"keyframes":         {"seq": "keyframes"},
	"trails":            {"base_step": "keyframes", "head_step": "keyframes"},
	"context_snapshots": {"source_step": "keyframes"},
}

// stepRemap records how an import renumbered the steps of each table.
type stepRemap struct {
	tx      *sql.Tx
	steps   map[string]map[int64]int64 // imported step -> destination step
	shifted map[string]bool            // an earlier row of the table moved
}

// apply rewrites the step columns of an imported row in place; rows of a
// table numbered by seq must arrive in step order. A row already present
// keeps its destination step, so importing it again compares it as the same
// row. A new row keeps its own step while that is free and no earlier row of
// its table moved; otherwise it is appended after the latest step, keeping
// the source order. Other step columns follow the renumbering of the table
// they refer to; a step that table does not have is kept.
func (r stepRemap) apply(ctx context.Context, table, key string, cols []string, values []any) error {
	for i, col := range cols {
		owner, ok := stepColumns[table][col]
		if !ok {
			continue
		}
		step, valid := values[i].(int64)
		if col != "seq" {
			if moved, found := r.steps[owner][step]; valid && found {
				values[i] = moved
			}
			continue
		}
		if !valid {
			return fmt.Errorf("source %s has an invalid step", table)
		}
		var id any
		if k := slices.Index(cols, key); k >= 0 {
			id = values[k]
		}
		moved, err := r.destinationStep(ctx, table, key, id, step)
		if err != nil {
			return fmt.Errorf("assigning imported %s step: %w", table, err)
		}
		if r.steps[table] == nil {
			r.steps[table] = map[int64]int64{}
		}
		r.steps[table][step] = moved
		r.shifted[table] = r.shifted[table] || moved != step
		values[i] = moved
	}
	return nil
}

func (r stepRemap) destinationStep(ctx context.Context, table, key string, id any, step int64) (int64, error) {
	var existing int64
	err := r.tx.QueryRowContext(ctx, "SELECT seq FROM "+table+" WHERE "+key+"=?", id).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var taken bool
	if err := r.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+table+" WHERE seq=?)", step).Scan(&taken); err != nil {
		return 0, err
	}
	if !taken && !r.shifted[table] {
		return step, nil
	}
	var next int64
	err = r.tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),-1)+1 FROM "+table).Scan(&next)
	return next, err
}

// mergeHistory validates the source before writes, reserves the destination
// writer and commits all rows together. Source blobs are immutable and verified
// before publication; a failed merge may leave only harmless unreferenced blobs.
func mergeHistory(ctx context.Context, root, source string) (importReport, error) {
	report := importReport{Schema: store.HistorySchemaVersion}
	src, err := openHistoryReadOnly(source)
	if err != nil {
		return report, err
	}
	defer src.Close()
	read, err := src.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return report, err
	}
	defer read.Rollback()
	var integrity string
	if err := read.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		return report, fmt.Errorf("source history failed integrity validation")
	}
	available := map[string][]string{}
	for _, table := range store.ImportTables() {
		rows, err := read.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			return report, err
		}
		var names []string
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var def any
			if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
				rows.Close()
				return report, err
			}
			names = append(names, name)
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return report, err
		}
		if len(names) == 0 && (table == "sessions" || table == "turns" || table == "keyframes" || table == "ops" || table == "commits") {
			return report, fmt.Errorf("source is missing required history table %s", table)
		}
		available[table] = names
	}
	dest, err := store.Open(filepath.Join(root, ".trace", "trace.db"))
	if err != nil {
		return report, err
	}
	defer dest.Close()
	write, err := dest.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer write.Rollback()
	if _, err = write.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); err != nil {
		return report, err
	}
	remap := stepRemap{tx: write, steps: map[string]map[int64]int64{}, shifted: map[string]bool{}}
	for _, table := range store.ImportTables() {
		if len(available[table]) == 0 {
			continue
		}
		supported := map[string]bool{}
		for _, col := range store.HistoryColumns[table] {
			supported[col] = true
		}
		var cols []string
		for _, col := range available[table] {
			if supported[col] {
				cols = append(cols, col)
			} else {
				return report, fmt.Errorf("source %s has unsupported column %s; upgrade Trace before importing", table, col)
			}
		}
		var sizes []string
		for _, col := range cols {
			sizes = append(sizes, "coalesce(length(CAST("+col+" AS BLOB)),0)")
		}
		var oversized int
		if err := read.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+table+" WHERE "+strings.Join(sizes, "+")+">16777216 LIMIT 1)").Scan(&oversized); err != nil {
			return report, err
		}
		if oversized != 0 {
			return report, fmt.Errorf("source %s contains a row exceeding 16 MiB", table)
		}
		order := ""
		if stepColumns[table]["seq"] == table && slices.Contains(cols, "seq") {
			order = " ORDER BY seq"
		}
		rows, err := read.QueryContext(ctx, "SELECT "+strings.Join(cols, ",")+" FROM "+table+order)
		if err != nil {
			return report, fmt.Errorf("reading %s: %w", table, err)
		}
		key := "id"
		if table == "commits" {
			key = "sha"
		}
		query := "INSERT INTO " + table + " (" + strings.Join(cols, ",") + ") VALUES (" + strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",") + ") ON CONFLICT(" + key + ") DO NOTHING"
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return report, err
			}
			for i, col := range cols {
				switch col {
				case "prompt", "reasoning", "tool_calls", "files_read", "detail", "content", "body", "title", "actor":
					if text, ok := values[i].(string); ok {
						values[i] = (redact.Redactor{}).Text(text)
					}
				}
			}
			for i, col := range cols {
				if (table == "sync_updates" && col == "data") || (table == "context_snapshots" && col == "payload") {
					var raw []byte
					switch v := values[i].(type) {
					case string:
						raw = []byte(v)
					case []byte:
						raw = v
					default:
						rows.Close()
						return report, fmt.Errorf("invalid imported JSON metadata")
					}
					cleaned, err := (redact.Redactor{}).JSON(raw)
					if err != nil {
						rows.Close()
						return report, fmt.Errorf("invalid imported JSON metadata: %w", err)
					}
					values[i] = string(cleaned)
					if table == "context_snapshots" {
						sum := sha256.Sum256(cleaned)
						for j, name := range cols {
							if name == "fingerprint" {
								values[j] = hex.EncodeToString(sum[:])
							}
						}
					}
				}
			}
			if err := remap.apply(ctx, table, key, cols, values); err != nil {
				rows.Close()
				return report, err
			}
			result, err := write.ExecContext(ctx, query, values...)
			if err != nil {
				rows.Close()
				return report, fmt.Errorf("importing %s (no rows committed): %w", table, err)
			}
			n, err := result.RowsAffected()
			if err != nil {
				rows.Close()
				return report, err
			}
			report.Imported += n
			if n == 0 {
				existing := make([]any, len(cols))
				pointers := make([]any, len(cols))
				var rowID any
				for i, col := range cols {
					pointers[i] = &existing[i]
					if col == key {
						rowID = values[i]
					}
				}
				if err := write.QueryRowContext(ctx, "SELECT "+strings.Join(cols, ",")+" FROM "+table+" WHERE "+key+"=?", rowID).Scan(pointers...); err != nil {
					rows.Close()
					return report, err
				}
				if !reflect.DeepEqual(existing, values) {
					rows.Close()
					return report, fmt.Errorf("conflicting %s row; import into a fresh checkout to preserve both histories", table)
				}
				report.Skipped++
			}
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return report, err
		}
	}
	if err := copyHistoryObjects(ctx, read, filepath.Join(filepath.Dir(source), "objects"), filepath.Join(root, ".trace", "objects")); err != nil {
		return report, err
	}
	if err := write.Commit(); err != nil {
		return report, fmt.Errorf("committing imported history: %w", err)
	}
	return report, nil
}

// historyHashes lists the objects a history references. Manifests are read
// from bs, or from the first of more that holds them.
func historyHashes(db interface {
	Query(string, ...any) (*sql.Rows, error)
}, bs *blob.Store, more ...*blob.Store) ([]blob.Hash, error) {
	rows, err := db.Query("SELECT manifest_hash FROM keyframes UNION SELECT before_hash FROM ops WHERE before_hash IS NOT NULL AND before_hash!='' UNION SELECT after_hash FROM ops WHERE after_hash IS NOT NULL AND after_hash!=''")
	if err != nil {
		return nil, err
	}
	set := map[blob.Hash]bool{}
	var manifests []blob.Hash
	// Manifest hashes need expansion; operation blobs are harmless to attempt as
	// JSON only when explicitly read from the keyframes table below.
	for rows.Next() {
		var h blob.Hash
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return nil, err
		}
		set[h] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	rows, err = db.Query("SELECT manifest_hash FROM keyframes")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h blob.Hash
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return nil, err
		}
		manifests = append(manifests, h)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, h := range manifests {
		m, err := manifest.Load(h, bs)
		for _, other := range more {
			if err == nil {
				break
			}
			m, err = manifest.Load(h, other)
		}
		if err != nil {
			return nil, err
		}
		for path, entry := range m {
			if !manifest.Portable(path) {
				return nil, fmt.Errorf("unsafe path in imported history")
			}
			set[entry.Hash] = true
		}
	}
	var hashes []blob.Hash
	for h := range set {
		hashes = append(hashes, h)
	}
	return hashes, nil
}

func copyHistoryObjects(ctx context.Context, tx *sql.Tx, source, destination string) error {
	bs, err := blob.OpenReadOnly(source)
	if err != nil {
		return err
	}
	dest, err := blob.Open(destination)
	if err != nil {
		return err
	}
	hashes, err := historyHashes(tx, bs)
	if err != nil {
		return fmt.Errorf("validating imported objects: %w", err)
	}
	for _, h := range hashes {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, err := bs.Get(h)
		if err != nil {
			return fmt.Errorf("reading imported object: %w", err)
		}
		stored, putErr := dest.Put(r)
		closeErr := r.Close()
		if err := errors.Join(putErr, closeErr); err != nil {
			return err
		}
		if stored != h {
			return fmt.Errorf("imported object hash mismatch")
		}
	}
	return nil
}

// snapshotHistory includes committed WAL content without copying mutable
// database/WAL files at different points in time.
func snapshotHistory(ctx context.Context, source, target string) error {
	db, err := openHistoryReadOnly(source)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, "VACUUM INTO ?", target)
	return err
}
