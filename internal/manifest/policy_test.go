package manifest

import (
	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapturePolicyExcludesTrackedFilesAndBoundsReads(t *testing.T) {
	root := t.TempDir()
	if err := exec.Command("git", "init", "-q", root).Run(); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"keep.txt": "kept", "private.json": "credential fixture", ".traceignore": "private.json\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := exec.Command("git", "-C", root, "add", ".").Run(); err != nil {
		t.Fatal(err)
	}
	bs, err := blob.Open(filepath.Join(root, ".trace", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Scan(root, nil, bs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["private.json"]; ok {
		t.Fatal("tracked excluded file captured")
	}
	t.Setenv("TRACE_MAX_FILE_BYTES", "32")
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte(strings.Repeat("a", 33)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(m, root, []string{"keep.txt"}, nil, bs); err == nil || !strings.Contains(err.Error(), "TRACE_MAX_FILE_BYTES") {
		t.Fatalf("oversized file accepted: %v", err)
	}
	t.Setenv("TRACE_MAX_FILE_BYTES", "100")
	t.Setenv("TRACE_MAX_CAPTURE_BYTES", "2")
	if _, err := Scan(root, nil, bs); err == nil || !strings.Contains(err.Error(), "TRACE_MAX_CAPTURE_BYTES") {
		t.Fatalf("capture budget ignored: %v", err)
	}
}
