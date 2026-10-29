package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReadOnlyTools(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Baseline(); err != nil {
		t.Fatal(err)
	}
	ev := event.Event{Agent: "codex", AgentSessionID: "fixture", Worktree: root, At: time.Now().UnixMilli()}
	ev.Kind = event.SessionStart
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	ev.Kind = event.TurnStart
	ev.Prompt = "implement API_TOKEN=abcdefghijklmnopqrstuvwxyz012345"
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "main.go")
	if err := os.WriteFile(p, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ev.Kind = event.ToolEdit
	ev.Paths = []string{p}
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	ev.Kind = event.TurnEnd
	if err := e.Process(ev); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := New(e).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	defer serverSession.Close()
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_checkpoints", map[string]any{"limit": 5}},
		{"get_turn", map[string]any{"step": 1}},
		{"get_context", map[string]any{"path": "main.go"}},
		{"why_line", map[string]any{"path": "main.go", "line": 1}},
		{"search_history", map[string]any{"query": "implement"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("tool error: %v", result)
			}
			b, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "abcdefghijklmnopqrstuvwxyz012345") {
				t.Fatal("secret leaked")
			}
		})
	}
	if _, err := e.DB.Exec("INSERT INTO messages(id,session_id,turn_id,role,content,at) SELECT 'extra',s.id,t.id,'assistant','unique discovery signal',2 FROM sessions s JOIN turns t ON t.session_id=s.id LIMIT 1"); err != nil {
		t.Fatal(err)
	}
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "search_history", Arguments: map[string]any{
		"query": "discovery", "scope": "messages", "edited_path": "main.go",
	}})
	if err != nil || result.IsError {
		t.Fatalf("message search failed: %v, %v", err, result)
	}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"step":1`) {
		t.Fatalf("message search did not find edited turn: %s", b)
	}
}

func TestWhyLineGroupsOneEditAcrossOperationRows(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	e, err := engine.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	var hashes []string
	for _, text := range []string{"a\nB\n", "A\nB\n", "X\nA\nB\n"} {
		hash, err := e.Blobs.PutBytes([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, string(hash))
	}
	for _, op := range []struct {
		id, before, after string
		at                int
	}{
		{"prior", hashes[0], hashes[1], 1},
		{"later-1", hashes[1], hashes[2], 2},
		{"later-2", hashes[1], hashes[2], 2},
	} {
		if _, err := e.DB.Exec("INSERT INTO ops(id,path,kind,before_hash,after_hash,source,at) VALUES(?,?,?,?,?,?,?)",
			op.id, "file.txt", "modify", op.before, op.after, "watcher", op.at); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := New(e).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	defer serverSession.Close()
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "why_line", Arguments: map[string]any{"path": "file.txt", "line": 3}})
	if err != nil || result.IsError {
		t.Fatalf("why_line failed: %v, %v", err, result)
	}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "present at baseline") {
		t.Fatalf("unchanged line blamed on an edit: %s", b)
	}
	largeHash, err := e.Blobs.PutBytes([]byte(strings.Repeat("line\n", 210000)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO ops(id,path,kind,before_hash,after_hash,source,at) VALUES(?,?,?,?,?,?,?)",
		"large-blob", "file.txt", "modify", hashes[2], largeHash, "watcher", 3); err != nil {
		t.Fatal(err)
	}
	result, err = clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "why_line", Arguments: map[string]any{"path": "file.txt", "line": 3}})
	if err != nil || result.IsError {
		t.Fatalf("why_line with large blob failed: %v, %v", err, result)
	}
	b, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "origin uncertain") {
		t.Fatalf("large blob attributed to an edit: %s", b)
	}
}
