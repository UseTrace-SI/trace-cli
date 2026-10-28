package watch

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

func addDirs(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
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
		if err := w.Add(p); err != nil {
			return fmt.Errorf("watching %s: %w", p, err)
		}
		return nil
	})
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
				if info, err := os.Lstat(ev.Name); err == nil && info.IsDir() {
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
