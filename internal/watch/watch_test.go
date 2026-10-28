package watch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestAddDirsExcludesPrivatePaths(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"src", ".trace", ".aws", ".ssh"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := addDirs(w, root); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range w.WatchList() {
		seen[p] = true
	}
	for _, tc := range []struct {
		name string
		want bool
	}{{"", true}, {"src", true}, {".trace", false}, {".aws", false}, {".ssh", false}} {
		p := filepath.Join(root, tc.name)
		if seen[p] != tc.want {
			t.Fatalf("watch %s=%t want %t", tc.name, seen[p], tc.want)
		}
	}
}
