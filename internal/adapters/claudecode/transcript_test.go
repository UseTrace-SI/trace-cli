package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTranscriptCompleteLinesAndRedaction(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	secret := "ghp_" + strings.Repeat("a", 36)
	first := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"checking ` + secret + `"},{"type":"tool_use","name":"Read","input":{"file_path":"` + filepath.Join(root, "main.go") + `"}}]}}` + "\n"
	partial := `{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}`
	if err := os.WriteFile(path, []byte(first+partial), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, offset, err := ReadTranscript(path, 0, root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ctx.Reasoning, secret) || !strings.Contains(ctx.Reasoning, "redacted") {
		t.Fatalf("secret not redacted: %q", ctx.Reasoning)
	}
	if ctx.FilesRead != `["main.go"]` || !strings.Contains(ctx.ToolCalls, "Read") || offset != int64(len(first)) {
		t.Fatalf("context=%+v offset=%d", ctx, offset)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, _, err = ReadTranscript(path, offset, root)
	if err != nil || ctx.Reasoning != "Assistant response: done" {
		t.Fatalf("context=%+v err=%v", ctx, err)
	}
}

func TestReadTranscriptSkipsSecretFilePath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"` + filepath.Join(root, ".env") + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, _, err := ReadTranscript(path, 0, root)
	if err != nil || ctx.FilesRead != "" || ctx.ToolCalls != "" {
		t.Fatalf("context=%+v err=%v", ctx, err)
	}
}
