package restore

import (
	"fmt"
	"io"
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

func Apply(target manifest.Manifest, root string, ig manifest.Ignorer, bs *blob.Store) ([]string, error) {
	current, err := manifest.Scan(root, ig, bs)
	if err != nil {
		return nil, err
	}
	changed := Plan(current, target)
	for _, rel := range changed {
		if _, err := safePath(root, rel); err != nil {
			return nil, err
		}
	}
	for _, rel := range changed {
		p, _ := safePath(root, rel)
		entry, exists := target[rel]
		if !exists {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("removing %s: %w", rel, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", rel, err)
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
	return changed, nil
}
