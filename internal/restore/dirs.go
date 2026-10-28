package restore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

const createdDirsFile = "created-dirs.json"

func createdDirsPath(root string) string {
	return filepath.Join(root, ".trace", createdDirsFile)
}

func validCreatedDir(rel string) bool {
	return rel != "." && rel != ".." && !filepath.IsAbs(rel) &&
		!strings.HasPrefix(rel, "../") && rel == filepath.ToSlash(filepath.Clean(rel)) && !manifest.Excluded(rel)
}

func loadCreatedDirs(root string) (map[string]bool, error) {
	dirs := map[string]bool{}
	traceDir := filepath.Join(root, ".trace")
	info, err := os.Lstat(traceDir)
	if os.IsNotExist(err) {
		return dirs, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checking Trace directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("trace directory is not a directory")
	}
	path := createdDirsPath(root)
	info, err = os.Lstat(path)
	if os.IsNotExist(err) {
		return dirs, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checking created directories: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("created directories record is not a regular file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading created directories: %w", err)
	}
	var paths []string
	if err := json.Unmarshal(b, &paths); err != nil {
		return nil, fmt.Errorf("decoding created directories: %w", err)
	}
	for _, rel := range paths {
		if !validCreatedDir(rel) {
			return nil, fmt.Errorf("unsafe created directory %q", rel)
		}
		dirs[rel] = true
	}
	return dirs, nil
}

func saveCreatedDirs(root string, dirs map[string]bool) error {
	traceDir := filepath.Join(root, ".trace")
	if err := os.MkdirAll(traceDir, 0700); err != nil {
		return fmt.Errorf("creating Trace directory: %w", err)
	}
	info, err := os.Lstat(traceDir)
	if err != nil {
		return fmt.Errorf("checking Trace directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("trace directory is not a directory")
	}
	paths := make([]string, 0, len(dirs))
	for rel := range dirs {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	b, err := json.Marshal(paths)
	if err != nil {
		return fmt.Errorf("encoding created directories: %w", err)
	}
	f, err := os.CreateTemp(traceDir, ".created-dirs-*")
	if err != nil {
		return fmt.Errorf("creating directories record: %w", err)
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return fmt.Errorf("setting directories record mode: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return fmt.Errorf("writing directories record: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing directories record: %w", err)
	}
	if err := os.Rename(f.Name(), createdDirsPath(root)); err != nil {
		return fmt.Errorf("saving created directories: %w", err)
	}
	return nil
}

func missingParentDirs(root, rel string) ([]string, error) {
	var missing []string
	for dir := filepath.Dir(filepath.FromSlash(rel)); dir != "."; dir = filepath.Dir(dir) {
		info, err := os.Lstat(filepath.Join(root, dir))
		if os.IsNotExist(err) {
			missing = append(missing, filepath.ToSlash(dir))
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking restore directory: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("restore directory %q is not a directory", dir)
		}
		break
	}
	return missing, nil
}

func pruneCreatedDirs(root string, dirs map[string]bool) (bool, error) {
	paths := make([]string, 0, len(dirs))
	for rel := range dirs {
		paths = append(paths, rel)
	}
	sort.Slice(paths, func(i, j int) bool {
		iDepth, jDepth := strings.Count(paths[i], "/"), strings.Count(paths[j], "/")
		if iDepth != jDepth {
			return iDepth > jDepth
		}
		return paths[i] > paths[j]
	})
	dirty := false
	for _, rel := range paths {
		path, err := safePath(root, rel)
		if err != nil {
			delete(dirs, rel)
			dirty = true
			continue
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			delete(dirs, rel)
			dirty = true
			continue
		}
		if err != nil {
			return dirty, fmt.Errorf("checking created directory: %w", err)
		}
		if !info.IsDir() {
			delete(dirs, rel)
			dirty = true
			continue
		}
		if err := os.Remove(path); err != nil {
			if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
				continue
			}
			return dirty, fmt.Errorf("removing empty Trace directory %q: %w", rel, err)
		}
		delete(dirs, rel)
		dirty = true
	}
	return dirty, nil
}
