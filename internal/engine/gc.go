package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/id"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

type GCResult struct {
	RemovedKeyframes int  `json:"removed_keyframes"`
	RemovedBlobs     int  `json:"removed_blobs"`
	DryRun           bool `json:"dry_run"`
}

func (e *Engine) GC(keepDays int, dryRun bool) (GCResult, error) {
	if keepDays < 0 {
		return GCResult{}, fmt.Errorf("keep-days must be nonnegative")
	}
	result := GCResult{DryRun: dryRun}
	cutoff := time.Now().AddDate(0, 0, -keepDays).UnixMilli()
	rows, err := e.DB.Query(`SELECT k.id,COALESCE(k.turn_id,'') FROM keyframes k
		WHERE k.seq!=0 AND k.created_at<? AND NOT EXISTS(SELECT 1 FROM commits c WHERE c.keyframe_id=k.id)`, cutoff)
	if err != nil {
		return result, fmt.Errorf("finding old keyframes: %w", err)
	}
	type victim struct{ id, turnID string }
	var victims []victim
	for rows.Next() {
		var v victim
		if err := rows.Scan(&v.id, &v.turnID); err != nil {
			rows.Close()
			return result, err
		}
		victims = append(victims, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	result.RemovedKeyframes = len(victims)
	if dryRun {
		return result, nil
	}
	// Keep a full scan of the current tree before removing historical snapshots.
	current, err := manifest.Scan(e.Root, nil, e.Blobs)
	if err != nil {
		return result, err
	}
	h, err := current.Save(e.Blobs)
	if err != nil {
		return result, err
	}
	keyID, err := id.New()
	if err != nil {
		return result, err
	}
	var seq int64
	if err := e.DB.QueryRow("SELECT COALESCE(MAX(seq),0)+1 FROM keyframes").Scan(&seq); err != nil {
		return result, err
	}
	if _, err := e.DB.Exec("INSERT INTO keyframes(id,seq,manifest_hash,git_head,tag,created_at) VALUES(?,?,?,?,?,?)", keyID, seq, h, e.head(), "gc-safety", time.Now().UnixMilli()); err != nil {
		return result, fmt.Errorf("saving GC safety keyframe: %w", err)
	}
	tx, err := e.DB.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	for _, v := range victims {
		if v.turnID != "" {
			if _, err := tx.Exec("DELETE FROM sync_updates WHERE table_name='ops' AND row_id IN (SELECT id FROM ops WHERE turn_id=?)", v.turnID); err != nil {
				return result, fmt.Errorf("removing obsolete operation updates: %w", err)
			}
			if _, err := tx.Exec("DELETE FROM sync_updates WHERE table_name='turns' AND row_id=?", v.turnID); err != nil {
				return result, fmt.Errorf("removing obsolete turn update: %w", err)
			}
			if _, err := tx.Exec("DELETE FROM messages WHERE turn_id=?", v.turnID); err != nil {
				return result, fmt.Errorf("removing obsolete messages: %w", err)
			}
			if _, err := tx.Exec("DELETE FROM ops WHERE turn_id=?", v.turnID); err != nil {
				return result, err
			}
		}
		if _, err := tx.Exec("DELETE FROM keyframes WHERE id=?", v.id); err != nil {
			return result, err
		}
		if v.turnID != "" {
			if _, err := tx.Exec("DELETE FROM turns WHERE id=?", v.turnID); err != nil {
				return result, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	marked := map[blob.Hash]bool{}
	rows, err = e.DB.Query("SELECT manifest_hash FROM keyframes")
	if err != nil {
		return result, err
	}
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return result, err
		}
		hashes = append(hashes, h)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	for _, hash := range hashes {
		marked[blob.Hash(hash)] = true
		m, err := manifest.Load(blob.Hash(hash), e.Blobs)
		if err != nil {
			return result, err
		}
		for _, entry := range m {
			marked[entry.Hash] = true
		}
	}
	rows, err = e.DB.Query("SELECT COALESCE(before_hash,''),COALESCE(after_hash,'') FROM ops")
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			rows.Close()
			return result, err
		}
		if a != "" {
			marked[blob.Hash(a)] = true
		}
		if b != "" {
			marked[blob.Hash(b)] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	parents := make([]blob.Hash, 0, len(marked))
	for hash := range marked {
		parents = append(parents, hash)
	}
	for _, hash := range parents {
		children, err := e.Blobs.Chunks(hash)
		if err != nil {
			return result, fmt.Errorf("marking chunks for %s: %w", hash, err)
		}
		for _, child := range children {
			marked[child] = true
		}
	}
	objects := filepath.Join(e.Root, ".trace", "objects")
	err = filepath.WalkDir(objects, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(objects, p)
		if err != nil {
			return err
		}
		dir, file := filepath.Split(rel)
		prefix := filepath.Base(filepath.Clean(dir))
		hash := blob.Hash(prefix + file)
		if len(hash) != 64 || marked[hash] {
			return nil
		}
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("removing unused blob: %w", err)
		}
		result.RemovedBlobs++
		return nil
	})
	if err != nil {
		return result, err
	}
	e.current = current
	if err := os.WriteFile(filepath.Join(e.Root, ".trace", "force-scan"), []byte("1\n"), 0600); err != nil {
		return result, fmt.Errorf("marking full scan after GC: %w", err)
	}
	return result, nil
}
