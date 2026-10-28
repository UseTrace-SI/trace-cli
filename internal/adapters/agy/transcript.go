package agy

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
)

type transcriptStep struct {
	Type      string `json:"type"`
	Status    string `json:"status"`
	Content   string `json:"content"`
	Thinking  string `json:"thinking"`
	ToolCalls []struct {
		Name string `json:"name"`
		Args struct {
			AbsolutePath string `json:"AbsolutePath"`
			TargetFile   string `json:"TargetFile"`
		} `json:"args"`
	} `json:"tool_calls"`
}

type ToolCall struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

type TranscriptContext struct {
	Prompt       string
	Reasoning    string
	ToolCalls    string
	FilesRead    string
	WrittenPaths []string
	HasCommand   bool
}

func ExpandTranscriptPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func userRequest(content string) string {
	if _, rest, ok := strings.Cut(content, "<USER_REQUEST>"); ok {
		if prompt, _, ok := strings.Cut(rest, "</USER_REQUEST>"); ok {
			return strings.TrimSpace(prompt)
		}
	}
	return strings.TrimSpace(content)
}

// LatestPrompt finds the last complete user input and its byte offset. Reading
// from that offset at Stop keeps prior turns out of the current keyframe.
func LatestPrompt(path string) (string, int64, error) {
	f, err := os.Open(ExpandTranscriptPath(path))
	if os.IsNotExist(err) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("opening Antigravity transcript: %w", err)
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var offset, latest int64
	var prompt string
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, fmt.Errorf("reading Antigravity transcript: %w", err)
		}
		var step transcriptStep
		if json.Unmarshal(line, &step) == nil && step.Type == "USER_INPUT" && step.Status == "DONE" {
			latest = offset
			prompt = userRequest(step.Content)
		}
		offset += int64(len(line))
	}
	if prompt == "" {
		return "", offset, nil
	}
	return prompt, latest, nil
}

func ReadTranscript(path string, offset int64, root string) (TranscriptContext, int64, error) {
	var result TranscriptContext
	if path == "" || filepath.Ext(path) != ".jsonl" {
		return result, offset, nil
	}
	f, err := os.Open(ExpandTranscriptPath(path))
	if os.IsNotExist(err) {
		return result, offset, nil
	}
	if err != nil {
		return result, offset, fmt.Errorf("opening Antigravity transcript: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return result, offset, fmt.Errorf("statting Antigravity transcript: %w", err)
	}
	if offset < 0 || offset > info.Size() {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return result, offset, fmt.Errorf("seeking Antigravity transcript: %w", err)
	}
	reader := bufio.NewReader(f)
	r := redact.Redactor{}
	var notes []string
	var calls []ToolCall
	readFiles, writtenFiles := map[string]bool{}, map[string]bool{}
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF { // Retry a partial final record later.
			break
		}
		if err != nil {
			return result, offset, fmt.Errorf("reading Antigravity transcript: %w", err)
		}
		offset += int64(len(line))
		var step transcriptStep
		if json.Unmarshal(line, &step) != nil || step.Status != "DONE" {
			continue
		}
		switch step.Type {
		case "USER_INPUT":
			if result.Prompt == "" {
				result.Prompt = r.Text(userRequest(step.Content))
			}
		case "PLANNER_RESPONSE":
			if step.Thinking != "" {
				notes = append(notes, r.Text(step.Thinking))
			}
			if step.Content != "" {
				notes = append(notes, "Assistant response: "+r.Text(step.Content))
			}
			for _, tool := range step.ToolCalls {
				path := tool.Args.TargetFile
				if path == "" {
					path = tool.Args.AbsolutePath
				}
				if manifest.SecretPath(path) {
					continue
				}
				call := ToolCall{Name: r.Text(tool.Name)}
				if path != "" {
					if !filepath.IsAbs(path) {
						path = filepath.Join(root, path)
					}
					rel, err := filepath.Rel(root, path)
					if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
						call.Path = filepath.ToSlash(rel)
					}
				}
				calls = append(calls, call)
				switch tool.Name {
				case "view_file":
					if call.Path != "" {
						readFiles[call.Path] = true
					}
				case "write_to_file", "replace_file_content", "multi_replace_file_content":
					if call.Path != "" {
						writtenFiles[call.Path] = true
					}
				case "run_command":
					result.HasCommand = true
				}
			}
		}
	}
	result.Reasoning = strings.Join(notes, "\n\n")
	if len(calls) > 0 {
		b, err := json.Marshal(calls)
		if err != nil {
			return result, offset, fmt.Errorf("encoding Antigravity tool calls: %w", err)
		}
		result.ToolCalls = string(b)
	}
	if len(readFiles) > 0 {
		paths := sortedPaths(readFiles)
		b, err := json.Marshal(paths)
		if err != nil {
			return result, offset, fmt.Errorf("encoding Antigravity read paths: %w", err)
		}
		result.FilesRead = string(b)
	}
	result.WrittenPaths = sortedPaths(writtenFiles)
	return result, offset, nil
}

func sortedPaths(paths map[string]bool) []string {
	out := make([]string, 0, len(paths))
	for path := range paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}
