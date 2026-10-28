package manifest

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
)

func TestScanUpdateSave(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "one")
	write("b.txt", "two")
	write(".env", "secret")
	write(".gitignore", "ignored.txt\nbuild/\n")
	write("ignored.txt", "ignore")
	if err := os.MkdirAll(filepath.Join(root, "build"), 0700); err != nil {
		t.Fatal(err)
	}
	write("build/output.bin", "generated")
	bs, err := blob.Open(filepath.Join(root, ".trace", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Scan(root, nil, bs)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 {
		t.Fatalf("manifest: %v", first)
	}
	write("a.txt", "changed")
	write("b.txt", "changed too")
	second, err := Update(first, root, []string{"a.txt", "b.txt"}, nil, bs)
	if err != nil {
		t.Fatal(err)
	}
	_, modified, _ := Diff(first, second)
	if !reflect.DeepEqual(modified, []string{"a.txt", "b.txt"}) {
		t.Fatalf("modified: %v", modified)
	}
	if _, err := Update(second, root, []string{filepath.Join(root, "..", "outside.txt")}, nil, bs); err == nil {
		t.Fatal("outside dirty path accepted")
	}
	h, err := second.Save(bs)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(h, bs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, loaded) {
		t.Fatalf("round trip: %v != %v", second, loaded)
	}
	h2, err := loaded.Save(bs)
	if err != nil || h != h2 {
		t.Fatalf("non-canonical manifest: %s %s %v", h, h2, err)
	}
}

func TestSecretPath(t *testing.T) {
	for _, tc := range []struct {
		path   string
		secret bool
	}{{".env", true}, {"sub/.env.local", true}, {".aws/credentials", true}, {"id_rsa.pub", true}, {"key.pem", true}, {"app.go", false}} {
		if got := SecretPath(tc.path); got != tc.secret {
			t.Errorf("%q: %v", tc.path, got)
		}
	}
}
