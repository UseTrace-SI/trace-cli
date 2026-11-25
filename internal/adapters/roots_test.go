package adapters

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

func TestAuggieHookRootUsesSessionCWD(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"cwd": nested})
	if err != nil {
		t.Fatal(err)
	}
	want, err := repo.FindRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := auggieHookRoot(payload); err != nil || got != want {
		t.Fatalf("got root %q, error %v; want %q", got, err, want)
	}
	payload, err = json.Marshal(map[string]any{"cwd": t.TempDir(), "workspace_roots": []string{nested}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := auggieHookRoot(payload); err != nil || got != want {
		t.Fatalf("workspace root: got %q, error %v; want %q", got, err, want)
	}
	payload, err = json.Marshal(map[string]any{"workspace_roots": []string{root, nested}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := auggieHookRoot(payload); err != nil || got != want {
		t.Fatalf("same worktree: got %q, error %v; want %q", got, err, want)
	}
	other := t.TempDir()
	if out, err := exec.Command("git", "-C", other, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	payload, err = json.Marshal(map[string]any{"workspace_roots": []string{root, other}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := auggieHookRoot(payload); err == nil {
		t.Errorf("unexpected root %q for separate worktrees", got)
	}
	for _, body := range []string{`{}`, `{"cwd":"relative"}`, `{"cwd":"/nonexistent/trace-repo"}`, `{"cwd":"/tmp","workspace_roots":["relative"]}`, `{`} {
		if got, err := auggieHookRoot([]byte(body)); err == nil {
			t.Errorf("unexpected root %q for %s", got, body)
		}
	}
}

func TestGooseProjectRoot(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	plugin := filepath.Join(root, ".agents", "plugins", "trace")
	if err := os.MkdirAll(plugin, 0700); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := gooseProjectRoot(plugin); err != nil || got != want {
		t.Fatalf("root = %q, %v; want %q", got, err, want)
	}
	for _, path := range []string{"relative/.agents/plugins/trace", filepath.Join(root, ".agents", "plugins", "other"), filepath.Join(root, "nested", ".agents", "plugins", "trace"), filepath.Join(t.TempDir(), ".agents", "plugins", "trace")} {
		if got, err := gooseProjectRoot(path); err == nil {
			t.Errorf("unexpected root %q for %q", got, path)
		}
	}
}

func TestKiroHookRootUsesPayloadDirectory(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"cwd": nested})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := kiroHookRoot(data); err != nil || got != want {
		t.Fatalf("root = %q, %v; want %q", got, err, want)
	}
	for _, body := range []string{`{"cwd":"relative"}`, `{"cwd":"/nonexistent/trace-kiro"}`, `{`} {
		if got, err := kiroHookRoot([]byte(body)); err == nil {
			t.Errorf("unexpected root %q for %s", got, body)
		}
	}
}

func TestOpenHandsHookRootUsesPayloadDirectory(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"working_dir": nested})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := openhandsHookRoot(data); err != nil || got != want {
		t.Fatalf("root = %q, %v; want %q", got, err, want)
	}
	for _, body := range []string{`{"working_dir":"relative"}`, `{"working_dir":"/nonexistent/trace-openhands"}`, `{`} {
		if got, err := openhandsHookRoot([]byte(body)); err == nil {
			t.Errorf("unexpected root %q for %s", got, body)
		}
	}
}
