package restore

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

// distinctLines returns content large enough to be stored in several chunks.
func distinctLines(n int) []byte {
	var b []byte
	for i := 0; i < n; i++ {
		b = fmt.Appendf(b, "line %06d\n", i)
	}
	return b
}

// A restore removes obsolete files before writing replacements, so a missing
// object (an interrupted GC, a partial import) must be found before anything
// is removed.
func TestApplyChecksObjectsBeforeChangingTree(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		// lost names the stored file of object h that goes missing.
		lost func(t *testing.T, objects string, bs *blob.Store, h blob.Hash) string
	}{
		{"missing object", []byte("restored\n"), func(t *testing.T, objects string, _ *blob.Store, h blob.Hash) string {
			return filepath.Join(objects, string(h[:2]), string(h[2:]))
		}},
		{"missing chunk", distinctLines(20000), func(t *testing.T, objects string, bs *blob.Store, h blob.Hash) string {
			chunks, err := bs.Chunks(h)
			if err != nil || len(chunks) < 2 {
				t.Fatalf("precondition: object is not stored in several chunks: %v, %v", chunks, err)
			}
			last := chunks[len(chunks)-1]
			return filepath.Join(objects, "chunks", string(last[:2]), string(last[2:]))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %s: %v", out, err)
			}
			objects := filepath.Join(root, ".trace", "objects")
			bs, err := blob.Open(objects)
			if err != nil {
				t.Fatal(err)
			}
			h, err := bs.PutBytes(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(tc.lost(t, objects, bs, h)); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{"obsolete.txt": "unsaved work\n", "kept.txt": "current\n"}
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Apply(manifest.Manifest{"kept.txt": {Hash: h, Mode: 0600}}, root, nil, bs); err == nil {
				t.Fatal("restore succeeded without its object")
			}
			for name, body := range files {
				if b, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(b) != body {
					t.Errorf("failed restore changed %s: %q, %v", name, b, err)
				}
			}
		})
	}
}
