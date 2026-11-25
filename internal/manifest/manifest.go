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
	"unicode"

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

// excluded reports paths Trace never captures or restores: Trace's own and
// agents' hook configuration (see protectedFolded) and secret files.
func excluded(rel string) bool {
	folded := foldPath(rel)
	return protectedFolded(folded) || secretFolded(folded)
}

// protectedFolded reports Git metadata, Trace state and agent hook
// configuration. Restored paths can come from an imported bundle or a recovery
// server, so Git metadata is protected at every depth (a nested repository's
// hooks run on its next commit). Names are compared case-insensitively, as
// case-insensitive file systems (the macOS default) resolve `.GIT` to `.git`.
func protectedFolded(folded string) bool {
	if folded == ".git" || strings.HasPrefix(folded, ".git/") || strings.HasSuffix(folded, "/.git") || strings.Contains(folded, "/.git/") {
		return true
	}
	for _, dir := range []string{".trace", ".trace-brain", ".trace-graph"} {
		if folded == dir || strings.HasPrefix(folded, dir+"/") {
			return true
		}
	}
	return folded == ".mcp.json" || folded == ".claude/settings.local.json" || folded == ".cursor/hooks.json" || folded == ".gemini/settings.json" || folded == ".agents/hooks.json"
}

// foldPath lowercases rel and drops the code points HFS+ ignores in names,
// which would otherwise let `.g‌it` reach `.git`.
func foldPath(rel string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == 0x200c || r == 0x200d || r == 0x200e || r == 0x200f, r >= 0x202a && r <= 0x202e,
			r >= 0x206a && r <= 0x206f, r == 0xfeff:
			return -1
		}
		return unicode.ToLower(r)
	}, rel)
}

func Excluded(rel string) bool { return excluded(rel) }

// WithoutExcluded drops the paths Trace never captures or restores. Keyframes
// recorded before an exclusion was added can still list such a path.
func (m Manifest) WithoutExcluded() Manifest {
	var out Manifest
	for rel := range m {
		if !excluded(rel) {
			continue
		}
		if out == nil {
			out = make(Manifest, len(m))
			for p, entry := range m {
				out[p] = entry
			}
		}
		delete(out, rel)
	}
	if out == nil {
		return m
	}
	return out
}

// Portable reports whether a snapshot path from another history (an imported
// bundle or a recovery server) is structurally safe to keep: relative,
// canonical, inside the repository and not Git metadata, Trace state or hook
// configuration. It deliberately accepts secret files: history recorded before
// a secret exclusion was added can list one, and restores skip them anyway.
func Portable(rel string) bool {
	return rel != "" && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, "../") &&
		!strings.ContainsAny(rel, "\\\x00") && rel == filepath.ToSlash(filepath.Clean(rel)) && !protectedFolded(foldPath(rel))
}

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

// listRepoFiles asks Git for tracked and untracked-but-not-ignored files. This
// avoids walking large ignored trees (node_modules, build output) during a full
// scan, and keeps .gitignore handling consistent with Update.
func listRepoFiles(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("listing repository files: %w", err)
	}
	var files []string
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		files = append(files, filepath.ToSlash(string(raw)))
	}
	return files, nil
}

// symlinkedParent reports whether a directory between root and rel is a
// symbolic link, the rule restore.safePath applies. Git keeps listing an index
// entry after its directory is replaced by a link, and reading through the
// link would capture a file from outside the repository. checked caches the
// answer for each directory.
func symlinkedParent(root, rel string, checked map[string]bool) (bool, error) {
	for i := 0; i < len(rel); i++ {
		if rel[i] != '/' {
			continue
		}
		dir := rel[:i]
		linked, ok := checked[dir]
		if !ok {
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(dir)))
			if os.IsNotExist(err) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			linked = info.Mode()&os.ModeSymlink != 0
			checked[dir] = linked
		}
		if linked {
			return true, nil
		}
	}
	return false, nil
}

func Scan(root string, ig Ignorer, bs *blob.Store) (Manifest, error) {
	paths, err := listRepoFiles(root)
	if err != nil {
		return nil, err
	}
	policy, err := loadPolicy(root)
	if err != nil {
		return nil, err
	}
	var total int64
	m := Manifest{}
	checked := map[string]bool{}
	for _, rel := range paths {
		if excluded(rel) || policy.excluded(rel) || (ig != nil && ig.Ignored(rel, false)) {
			continue
		}
		linked, err := symlinkedParent(root, rel, checked)
		if err != nil {
			return nil, fmt.Errorf("statting %s: %w", rel, err)
		}
		if linked {
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("statting %s: %w", rel, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := policy.check(root, rel, info.Size(), total); err != nil {
			return nil, err
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", rel, err)
		}
		reader := &boundedFile{Reader: f, remaining: min(policy.fileBytes, policy.totalBytes-total)}
		h, putErr := bs.Put(reader)
		total += reader.read
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
	policy, err := loadPolicy(root)
	if err != nil {
		return nil, err
	}
	var total int64
	m := clone(prev)
	// Old checkpoints may contain state written before an exclusion existed.
	// Drop it from new snapshots without modifying historical checkpoints.
	for rel := range m {
		if excluded(rel) || policy.excluded(rel) {
			delete(m, rel)
		}
	}
	if len(dirty) == 0 {
		return m, nil
	}
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
		if excluded(rel) || policy.excluded(rel) || (ig != nil && ig.Ignored(rel, false)) || seen[rel] {
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
		if err := policy.check(root, rel, info.Size(), total); err != nil {
			return nil, err
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", rel, err)
		}
		reader := &boundedFile{Reader: f, remaining: min(policy.fileBytes, policy.totalBytes-total)}
		h, putErr := bs.Put(reader)
		total += reader.read
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

// StripExcluded returns the manifest stored at h without the paths Trace
// excludes, for sharing history beyond this machine. A keyframe recorded
// before an exclusion was added can still list such a file, and its bytes
// must not leave with the history. When nothing is excluded it returns h
// unchanged; otherwise it saves the stripped manifest to bs and returns its
// hash with the dropped paths.
func StripExcluded(h blob.Hash, bs *blob.Store) (blob.Hash, []string, error) {
	m, err := Load(h, bs)
	if err != nil {
		return "", nil, err
	}
	var dropped []string
	for rel := range m {
		if excluded(rel) {
			dropped = append(dropped, rel)
		}
	}
	if len(dropped) == 0 {
		return h, nil, nil
	}
	for _, rel := range dropped {
		delete(m, rel)
	}
	stripped, err := m.Save(bs)
	if err != nil {
		return "", nil, err
	}
	sort.Strings(dropped)
	return stripped, dropped, nil
}

func Load(h blob.Hash, bs *blob.Store) (Manifest, error) {
	r, err := bs.Get(h)
	if err != nil {
		return nil, fmt.Errorf("loading manifest: %w", err)
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, 32*1024*1024+1))
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	if len(b) > 32*1024*1024 {
		return nil, fmt.Errorf("manifest exceeds 32 MiB")
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
