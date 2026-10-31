package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

// TranscriptContext is the context Claude has written since a turn began.
// Only assistant content is collected; user prompts are captured by the hook.
type TranscriptContext struct {
	Reasoning string
	ToolCalls string
	FilesRead string
	TokensIn  int64
	TokensOut int64
}

type transcriptRecord struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type transcriptToolCall struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// ReadTranscript reads complete JSONL records starting at offset. It returns
// the next complete-record offset so a partially written line is retried.
func ReadTranscript(path string, offset int64, root string) (TranscriptContext, int64, error) {
	var result TranscriptContext
	if path == "" || filepath.Ext(path) != ".jsonl" {
		return result, offset, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return result, offset, nil
	}
	if err != nil {
		return result, offset, fmt.Errorf("opening Claude transcript: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return result, offset, fmt.Errorf("statting Claude transcript: %w", err)
	}
	if offset < 0 || offset > info.Size() {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return result, offset, fmt.Errorf("seeking Claude transcript: %w", err)
	}
	reader := bufio.NewReader(f)
	var notes []string
	var calls []transcriptToolCall
	readFiles := map[string]bool{}
	var tokensIn, tokensOut int64
	r := redact.Redactor{}
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF { // Incomplete records are not consumed.
			break
		}
		if err != nil {
			return result, offset, fmt.Errorf("reading Claude transcript: %w", err)
		}
		offset += int64(len(line))
		var record transcriptRecord
		if json.Unmarshal(line, &record) != nil || record.Type != "assistant" {
			continue
		}
		tokensIn += record.Message.Usage.InputTokens
		tokensOut += record.Message.Usage.OutputTokens
		for _, block := range record.Message.Content {
			switch block.Type {
			case "thinking":
				if block.Thinking != "" {
					notes = append(notes, r.Text(block.Thinking))
				}
			case "text":
				if block.Text != "" {
					notes = append(notes, "Assistant response: "+r.Text(block.Text))
				}
			case "tool_use":
				var pathInput struct {
					FilePath string `json:"file_path"`
				}
				_ = json.Unmarshal(block.Input, &pathInput)
				if manifest.SecretPath(pathInput.FilePath) {
					continue
				}
				call := transcriptToolCall{Name: r.Text(block.Name)}
				if pathInput.FilePath != "" {
					rel, err := repo.RelativePath(root, pathInput.FilePath)
					if err == nil && !manifest.Excluded(rel) {
						call.Path = rel
					}
				}
				calls = append(calls, call)
				if block.Name == "Read" {
					if call.Path != "" {
						readFiles[call.Path] = true
					}
				}
			}
		}
	}
	result.Reasoning = strings.Join(notes, "\n\n")
	result.TokensIn = tokensIn
	result.TokensOut = tokensOut
	if len(calls) > 0 {
		b, err := json.Marshal(calls)
		if err != nil {
			return result, offset, fmt.Errorf("encoding Claude tool calls: %w", err)
		}
		result.ToolCalls = string(b)
	}
	if len(readFiles) > 0 {
		paths := make([]string, 0, len(readFiles))
		for path := range readFiles {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		b, err := json.Marshal(paths)
		if err != nil {
			return result, offset, fmt.Errorf("encoding Claude read paths: %w", err)
		}
		result.FilesRead = string(b)
	}
	return result, offset, nil
}
