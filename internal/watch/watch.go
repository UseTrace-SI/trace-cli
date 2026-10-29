package watch

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/fsnotify/fsnotify"
)

// addDirs watches every directory Git considers relevant. Asking git for the
// tracked and untracked-but-not-ignored files avoids descending into large
// ignored trees such as node_modules or build output.
func addDirs(w *fsnotify.Watcher, root string) error {
	dirs, err := dirsToWatch(root)
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if err := w.Add(dir); err != nil {
			// Do not abort the whole watcher when a single directory cannot be
			// watched (e.g. EMFILE / too many open files). Skip it and keep
			// capturing changes everywhere else.
			continue
		}
	}
	return nil
}

func dirsToWatch(root string) ([]string, error) {
	dirs := map[string]bool{root: true}
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return walkDirs(root)
	}
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		rel := filepath.ToSlash(string(raw))
		if manifest.Excluded(rel) {
			continue
		}
		dir := filepath.Join(root, filepath.Dir(filepath.FromSlash(rel)))
		if !insideRoot(root, dir) {
			continue
		}
		dirs[dir] = true
	}
	// Pre-existing empty directories are intentionally not discovered with a
	// full walk: walking ignored trees is exactly what this avoids. Directories
	// created while the daemon runs are added on their fsnotify create event.
	outDirs := make([]string, 0, len(dirs))
	for dir := range dirs {
		outDirs = append(outDirs, dir)
	}
	sort.Strings(outDirs)
	return outDirs, nil
}

func ignoredDir(root, rel string) bool {
	cmd := exec.Command("git", "-C", root, "check-ignore", "-q", "--", rel)
	return cmd.Run() == nil
}

func insideRoot(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func walkDirs(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel != "." && manifest.Excluded(filepath.ToSlash(rel)) {
			return filepath.SkipDir
		}
		dirs = append(dirs, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return dirs, nil
}

func Run(ctx context.Context, root string, onDirty func([]string)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer w.Close()
	if err := addDirs(w, root); err != nil {
		return err
	}
	set := map[string]bool{}
	var timer *time.Timer
	var tick <-chan time.Time
	flush := func() {
		if len(set) == 0 {
			return
		}
		paths := make([]string, 0, len(set))
		for p := range set {
			paths = append(paths, p)
		}
		set = map[string]bool{}
		onDirty(paths)
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				flush()
				return nil
			}
			rel, err := filepath.Rel(root, ev.Name)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if rel == ".." || strings.HasPrefix(rel, "../") || manifest.Excluded(rel) {
				continue
			}
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Lstat(ev.Name); err == nil && info.IsDir() && !ignoredDir(root, rel) {
					_ = addDirs(w, ev.Name)
				}
			}
			set[ev.Name] = true
			if timer == nil {
				timer = time.NewTimer(150 * time.Millisecond)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(150 * time.Millisecond)
			}
			tick = timer.C
		case <-tick:
			flush()
			tick = nil
		case _, ok := <-w.Errors:
			if !ok {
				flush()
				return nil
			}
		}
	}
}
