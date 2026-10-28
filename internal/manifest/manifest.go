package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
)

type Entry struct {
	Hash blob.Hash   `json:"hash"`
	Mode fs.FileMode `json:"mode"`
}
type Manifest map[string]Entry
type Ignorer interface {
	Ignored(rel string, isDir bool) bool
}

func excluded(rel string) bool {
	return rel == ".git" || rel == ".trace" || rel == ".mcp.json" || rel == ".claude/settings.local.json" || rel == ".cursor/hooks.json" || rel == ".gemini/settings.json" || rel == ".agents/hooks.json" || strings.HasPrefix(rel, ".git/") || strings.HasPrefix(rel, ".trace/") || SecretPath(rel)
}

func Excluded(rel string) bool { return excluded(rel) }

func gitIgnored(root string, paths []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(paths) == 0 {
		return out, nil
	}
	cmd := exec.Command("git", "-C", root, "check-ignore", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	b, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			return nil, fmt.Errorf("checking git excludes: %w", err)
		}
	}
	for _, p := range bytes.Split(b, []byte{0}) {
		if len(p) > 0 {
			out[string(p)] = true
		}
	}
	return out, nil
}

func Scan(root string, ig Ignorer, bs *blob.Store) (Manifest, error) {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if excluded(rel) || (ig != nil && ig.Ignored(rel, d.IsDir())) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking repository: %w", err)
	}
	ignored, err := gitIgnored(root, paths)
	if err != nil {
		return nil, err
	}
	m := Manifest{}
	for _, rel := range paths {
		if ignored[rel] {
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(p)
		if err != nil {
			return nil, fmt.Errorf("statting %s: %w", rel, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", rel, err)
		}
		h, putErr := bs.Put(f)
		closeErr := f.Close()
		if putErr != nil {
			return nil, fmt.Errorf("storing %s: %w", rel, putErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("closing %s: %w", rel, closeErr)
		}
		m[rel] = Entry{Hash: h, Mode: info.Mode().Perm()}
	}
	return m, nil
}

func Update(prev Manifest, root string, dirty []string, ig Ignorer, bs *blob.Store) (Manifest, error) {
	if len(dirty) == 0 {
		return clone(prev), nil
	}
	m := clone(prev)
	var rels []string
	seen := map[string]bool{}
	for _, path := range dirty {
		rel := path
		if filepath.IsAbs(path) {
			var err error
			rel, err = filepath.Rel(root, path)
			if err != nil {
				return nil, fmt.Errorf("relativizing dirty path %q: %w", path, err)
			}
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("invalid dirty path %q", path)
		}
		if excluded(rel) || (ig != nil && ig.Ignored(rel, false)) || seen[rel] {
			continue
		}
		seen[rel] = true
		rels = append(rels, rel)
	}
	ignored, err := gitIgnored(root, rels)
	if err != nil {
		return nil, err
	}
	for _, rel := range rels {
		if ignored[rel] {
			delete(m, rel)
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			delete(m, rel)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("statting %s: %w", rel, err)
		}
		if info.IsDir() {
			return Scan(root, ig, bs)
		}
		if !info.Mode().IsRegular() {
			delete(m, rel)
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", rel, err)
		}
		h, putErr := bs.Put(f)
		closeErr := f.Close()
		if putErr != nil {
			return nil, fmt.Errorf("storing %s: %w", rel, putErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("closing %s: %w", rel, closeErr)
		}
		m[rel] = Entry{Hash: h, Mode: info.Mode().Perm()}
	}
	return m, nil
}

func clone(m Manifest) Manifest {
	out := Manifest{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (m Manifest) Save(bs *blob.Store) (blob.Hash, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encoding manifest: %w", err)
	}
	return bs.PutBytes(b)
}

func Load(h blob.Hash, bs *blob.Store) (Manifest, error) {
	r, err := bs.Get(h)
	if err != nil {
		return nil, fmt.Errorf("loading manifest: %w", err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decoding manifest: %w", err)
	}
	return m, nil
}

func Diff(a, b Manifest) (added, modified, deleted []string) {
	for p, v := range b {
		if old, ok := a[p]; !ok {
			added = append(added, p)
		} else if old != v {
			modified = append(modified, p)
		}
	}
	for p := range a {
		if _, ok := b[p]; !ok {
			deleted = append(deleted, p)
		}
	}
	sort.Strings(added)
	sort.Strings(modified)
	sort.Strings(deleted)
	return
}
