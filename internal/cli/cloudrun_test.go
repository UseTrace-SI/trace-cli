package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
)

func TestCloudRunDryRunIsExplicitAndDoesNotNeedCredentials(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.js"), []byte("console.log('hello')"), 0600); err != nil {
		t.Fatal(err)
	}
	jsonOutput := true
	cmd := cloudRunCommand(&root, &jsonOutput)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--project", "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", "--include", "main.js", "--dry-run", "--", "node", "main.js"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["submitted"] != false || len(got["files"].([]any)) != 1 || strings.Contains(out.String(), "console.log") {
		t.Fatalf("unsafe preview: %s", &out)
	}
	for _, name := range []string{".env", ".git/config", "../escape", "key.p12", "key.pfx", "key.keystore", "bad\x01name"} {
		if _, _, err := cloudFiles(root, []string{name}); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("main.js\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cloudFiles(root, []string{"main.js"}); err == nil {
		t.Fatal("uploaded an ignored file")
	}
}

func TestCloudUploadOpensEachComponentWithoutFollowingSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "safe"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "safe", "file.txt"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("safe", filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("safe/file.txt", filepath.Join(root, "leaf")); err != nil {
		t.Fatal(err)
	}
	anchor, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer anchor.Close()
	file, err := openCloudFile(anchor, "safe/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	for _, name := range []string{"linked/file.txt", "leaf"} {
		file, err := openCloudFile(anchor, name)
		if err == nil {
			file.Close()
			t.Fatalf("followed symlink: %s", name)
		}
	}
}

type cloudTransport func(*http.Request) (*http.Response, error)

func (f cloudTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
func TestCloudRequestsBindPersonalCredentialsToTheConfiguredOriginAndRefuseRedirects(t *testing.T) {
	cfg := syncclient.Config{Endpoint: "https://trace.example", Remote: "solo/code", Mode: "personal"}
	token := "trace_" + strings.Repeat("x", 43)
	calls := 0
	client := &http.Client{Transport: cloudTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "trace.example" || request.Header.Get("Authorization") != "Bearer "+token {
			t.Fatal("credential boundary mismatch")
		}
		return &http.Response{StatusCode: 307, Header: http.Header{"Location": []string{"https://attacker.example/"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})}
	if _, err := cloudRequest(context.Background(), cfg, token, "POST", "/api/runner/jobs", map[string]any{}, client); err == nil {
		t.Fatal("followed redirect")
	}
	if calls != 1 {
		t.Fatal("forwarded credentials to another origin")
	}
	client.Transport = cloudTransport(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"job","status":"queued"}`)), Request: request}, nil
	})
	if body, err := cloudRequest(context.Background(), cfg, token, "POST", "/api/runner/jobs", map[string]any{}, client); err != nil || !json.Valid(body) {
		t.Fatalf("valid job response: %s %v", body, err)
	}
	if _, err := cloudRequest(context.Background(), cfg, "github-token", "POST", "/api/runner/jobs", nil, client); err == nil {
		t.Fatal("accepted legacy credentials")
	}
}
