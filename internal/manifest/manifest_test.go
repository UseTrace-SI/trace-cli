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

// Git keeps listing an index entry after its directory is replaced by a
// symlink. Scan must not read through the link (a link outside the repository
// would capture a foreign file); restore refuses such paths for the same reason.
func TestScanSkipsPathsBelowSymlinkedDirectories(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target func(root string) string
	}{
		{"link outside the repository", func(string) string { return t.TempDir() }},
		{"link inside the repository", func(root string) string { return filepath.Join(root, "real") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %s: %v", out, err)
			}
			for _, name := range []string{"cfg/credentials", "real/notes.txt", "app.go"} {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, name), []byte("placeholder\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Staging is enough: git ls-files --cached keeps the stale entry.
			if out, err := exec.Command("git", "-C", root, "add", ".").CombinedOutput(); err != nil {
				t.Fatalf("git add: %s: %v", out, err)
			}
			target := tc.target(root)
			if err := os.WriteFile(filepath.Join(target, "credentials"), []byte("aws_secret_access_key=LINKED\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(root, "cfg")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(root, "cfg")); err != nil {
				t.Fatal(err)
			}
			bs, err := blob.Open(filepath.Join(t.TempDir(), "objects"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := Scan(root, nil, bs)
			if err != nil {
				t.Fatal(err)
			}
			if entry, ok := got["cfg/credentials"]; ok {
				t.Fatalf("captured cfg/credentials through a symlinked directory (blob %s)", entry.Hash)
			}
			if got["app.go"].Hash == "" || got["real/notes.txt"].Hash == "" {
				t.Fatalf("lost an ordinary file: %v", got)
			}
		})
	}
}

// Every OpenSSH key type and key/certificate files in any letter case must
// stay out of snapshots, without catching ordinary source files.
func TestSecretPathMatchesKeyFilesInAnyCase(t *testing.T) {
	for _, tc := range []struct {
		path   string
		secret bool
	}{
		{"deploy/id_ed25519", true},
		{"id_ed25519.pub", true},
		{"keys/id_ecdsa", true},
		{"id_ecdsa_sk.pub", true},
		{"id_dsa", true},
		{"ID_RSA", true},
		{"certs/Server.PEM", true},
		{"TLS.KEY", true},
		{"tls.Key", true},
		{".SSH/config", true},
		{".AWS/credentials", true},
		{".ENV.local", true},
		{"internal/syncclient/credentials.go", false},
		{"internal/manifest/excludes.go", false},
		{"keys.go", false},
		{"docs/pem-format.md", false},
		{"src/Keyboard.tsx", false},
	} {
		if got := SecretPath(tc.path); got != tc.secret {
			t.Errorf("SecretPath(%q) = %v; want %v", tc.path, got, tc.secret)
		}
	}
}

// Restores write manifest paths that may come from an imported bundle or a
// recovery server. Git metadata must be protected at every depth, and on
// case-insensitive file systems (the macOS default) in every letter case.
func TestExcludedProtectsGitMetadataAndTraceStateInAnyCase(t *testing.T) {
	for _, tc := range []struct {
		path     string
		excluded bool
	}{
		{".git/config", true},
		{"sub/.git/hooks/pre-commit", true},
		{"vendor/lib/.git", true},
		{".GIT/hooks/pre-commit", true},
		{".Git/config", true},
		{".g\u200cit/config", true},
		{"sub/.GIT\ufeff/config", true},
		{".TRACE/trace.db", true},
		{".Trace-Brain/memory.db", true},
		{".MCP.json", true},
		{".Claude/settings.local.json", true},
		{".CURSOR/hooks.json", true},
		{".gitignore", false},
		{".github/workflows/ci.yml", false},
		{"docs/.gitkeep", false},
		{"src/git/client.go", false},
		{"pkg/.gitattributes", false},
		{".tracebook/notes.md", false},
	} {
		if got := Excluded(tc.path); got != tc.excluded {
			t.Errorf("Excluded(%q) = %v; want %v", tc.path, got, tc.excluded)
		}
	}
}

func TestCompanionStateExcludedFromAllNewSnapshots(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	paths := []string{".trace-brain/memory.db", ".trace-brain/memory.db-wal", ".trace-graph/graph.db", ".trace-graph/graph.db-shm"}
	for _, p := range append(paths, "app.go") {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte("private state"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Even accidentally tracked databases must be excluded before reading.
	if out, err := exec.Command("git", "-C", root, "add", "-f", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %v", out, err)
	}
	bs, err := blob.Open(filepath.Join(root, ".trace", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	scanned, err := Scan(root, nil, bs)
	if err != nil {
		t.Fatal(err)
	}
	if len(scanned) != 1 || scanned["app.go"].Hash == "" {
		t.Fatalf("generated data in scan: %v", scanned)
	}
	old := Manifest{"app.go": scanned["app.go"]}
	for _, p := range paths {
		old[p] = scanned["app.go"]
	}
	for _, dirty := range [][]string{nil, paths} {
		updated, err := Update(old, root, dirty, nil, bs)
		if err != nil || !reflect.DeepEqual(updated, scanned) {
			t.Fatalf("generated data in incremental snapshot: %v, %v", updated, err)
		}
	}
	if len(old) != 5 {
		t.Fatal("historical manifest was mutated")
	}
}
