package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A .mcp.json that is a symlink must stay a symlink after install/uninstall:
// the atomic write has to replace the resolved target, not the link itself.
func TestMCPInstallPreservesSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "shared-mcp.json")
	if err := os.WriteFile(target, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".mcp.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := installMCP(root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("installMCP replaced the .mcp.json symlink with a regular file")
	}
	var cfg mcpConfig
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Servers["trace"]; !ok {
		t.Fatalf("trace server not installed in symlink target: %s", b)
	}
	if _, ok := cfg.Servers["other"]; !ok {
		t.Fatalf("existing server lost from symlink target: %s", b)
	}

	if err := uninstallMCP(root); err != nil {
		t.Fatal(err)
	}
	info, err = os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("uninstallMCP replaced the .mcp.json symlink with a regular file")
	}
	b, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var after mcpConfig
	if err := json.Unmarshal(b, &after); err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Servers["trace"]; ok {
		t.Fatalf("trace server still installed after uninstall: %s", b)
	}
	if _, ok := after.Servers["other"]; !ok {
		t.Fatalf("existing server lost after uninstall: %s", b)
	}
}

// Atomic replacement must keep an existing file's mode and use 0600 for new
// files, matching the previous os.WriteFile behaviour.
func TestMCPWritePreservesMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		initial  []byte
		wantMode os.FileMode
	}{
		{name: "existing file keeps mode", initial: []byte(`{"mcpServers":{}}`), wantMode: 0640},
		{name: "new file is private", initial: nil, wantMode: 0600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".trace"), 0700); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(root, ".mcp.json")
			if tc.initial != nil {
				if err := os.WriteFile(p, tc.initial, tc.wantMode); err != nil {
					t.Fatal(err)
				}
			}
			if err := installMCP(root); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != tc.wantMode {
				t.Fatalf(".mcp.json mode = %o, want %o", got, tc.wantMode)
			}
		})
	}
}
