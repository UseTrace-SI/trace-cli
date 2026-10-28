package restore

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

func safePath(root, rel string) (string, error) {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) || manifest.Excluded(rel) {
		return "", fmt.Errorf("unsafe restore path %q", rel)
	}
	parts := strings.Split(rel, "/")
	p := root
	for _, part := range parts[:len(parts)-1] {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("checking restore path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink parent in restore path %q", rel)
		}
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

func Plan(current, target manifest.Manifest) []string {
	add, change, remove := manifest.Diff(current, target)
	paths := append(append(add, change...), remove...)
	sort.Strings(paths)
	return paths
}

// removeDirectoryTree removes only directories. A restore must never remove an
// ignored or untracked file that happens to live under a replaced path.
func removeDirectoryTree(root string) error {
	var dirs []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return fmt.Errorf("directory contains an untracked or ignored file: %s", path)
		}
		dirs = append(dirs, path)
		return nil
	}); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Remove(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

func Apply(target manifest.Manifest, root string, ig manifest.Ignorer, bs *blob.Store) ([]string, error) {
	current, err := manifest.Scan(root, ig, bs)
	if err != nil {
		return nil, err
	}
	return ApplyPlanned(target, root, Plan(current, target), bs)
}

// ApplyPlanned restores a plan computed from a saved manifest. Reusing the
// manifest avoids scanning and storing the working tree a second time.
func ApplyPlanned(target manifest.Manifest, root string, changed []string, bs *blob.Store) (applied []string, retErr error) {
	for _, rel := range changed {
		if _, err := safePath(root, rel); err != nil {
			return nil, err
		}
	}
	created, err := loadCreatedDirs(root)
	if err != nil {
		return nil, err
	}
	dirtyDirs := false
	defer func() {
		if dirtyDirs {
			if err := saveCreatedDirs(root, created); err != nil && retErr == nil {
				retErr = err
			}
		}
	}()
	// Remove obsolete files before writing replacements. A file may become a
	// directory (or the reverse) between keyframes, regardless of path order.
	for _, rel := range changed {
		if _, exists := target[rel]; exists {
			continue
		}
		p, err := safePath(root, rel)
		if err != nil {
			return nil, err
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("removing %s: %w", rel, err)
		}
	}
	for _, rel := range changed {
		entry, exists := target[rel]
		if !exists {
			continue
		}
		p, err := safePath(root, rel)
		if err != nil {
			return nil, err
		}
		if info, err := os.Lstat(p); err == nil && info.IsDir() {
			if err := removeDirectoryTree(p); err != nil {
				return nil, fmt.Errorf("removing directory for %s: %w", rel, err)
			}
			if created[rel] {
				delete(created, rel)
				dirtyDirs = true
			}
		} else if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("checking restore target %s: %w", rel, err)
		}
		missing, err := missingParentDirs(root, rel)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", rel, err)
		}
		for _, dir := range missing {
			if !created[dir] {
				created[dir] = true
				dirtyDirs = true
			}
		}
		r, err := bs.Get(entry.Hash)
		if err != nil {
			return nil, fmt.Errorf("loading %s: %w", rel, err)
		}
		tmp, err := os.CreateTemp(filepath.Dir(p), ".trace-restore-*")
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("creating temporary file for %s: %w", rel, err)
		}
		_, copyErr := io.Copy(tmp, r)
		r.Close()
		if copyErr != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return nil, fmt.Errorf("writing %s: %w", rel, copyErr)
		}
		if err := tmp.Chmod(entry.Mode.Perm()); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return nil, fmt.Errorf("setting mode on %s: %w", rel, err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmp.Name())
			return nil, fmt.Errorf("closing %s: %w", rel, err)
		}
		if err := os.Rename(tmp.Name(), p); err != nil {
			os.Remove(tmp.Name())
			return nil, fmt.Errorf("replacing %s: %w", rel, err)
		}
	}
	pruned, err := pruneCreatedDirs(root, created)
	dirtyDirs = dirtyDirs || pruned
	if err != nil {
		return nil, err
	}
	return changed, nil
}
