package poolside

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

var names = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"}

const beginMarker = "# BEGIN TRACE POOLSIDE HOOKS\n"
const endMarker = "# END TRACE POOLSIDE HOOKS\n"
const legacyMarker = "# Managed by Trace; edit through trace setup --agent poolside\n"

// isManagedBlock accepts an older Trace executable path while rejecting any
// other edits in the generated section.
func isManagedBlock(existing, expected string) bool {
	a, b := strings.Split(existing, "\n"), strings.Split(expected, "\n")
	if len(a) != len(b) {
		return false
	}
	idx := 0
	for i := range b {
		const prefix = "      command: "
		if strings.HasPrefix(b[i], prefix) {
			if !strings.HasPrefix(a[i], prefix) || idx >= len(names) {
				return false
			}
			var command string
			if json.Unmarshal([]byte(strings.TrimPrefix(a[i], prefix)), &command) != nil || !hookcmd.IsTraceHookFor(command, "poolside", names[idx]) || !hookcmd.IsTraceHook(command, "poolside") {
				return false
			}
			idx++
		} else if a[i] != b[i] {
			return false
		}
	}
	return idx == len(names)
}

// canAppendHooks only accepts ordinary block mappings without a hooks key.
// Ambiguous YAML is left untouched for manual merging.
func canAppendHooks(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if line[0] == ' ' {
			continue
		}
		if line[0] == '\t' {
			return false
		}
		i := strings.IndexByte(line, ':')
		if i <= 0 || (i+1 < len(line) && line[i+1] != ' ' && line[i+1] != '\t') {
			return false
		}
		key := line[:i]
		for _, r := range key {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return false
			}
		}
		if key == "hooks" {
			return false
		}
	}
	return true
}

// Setup preserves surrounding configuration and refuses ambiguous YAML or
// changes inside the managed block instead of overwriting user settings.
func Setup(home string, remove bool) error {
	p := filepath.Join(home, ".config", "poolside", "settings.yaml")
	var block strings.Builder
	block.WriteString(beginMarker + "hooks:\n")
	for _, name := range names {
		command, err := json.Marshal(hookcmd.Command("poolside", name))
		if err != nil {
			return err
		}
		fmt.Fprintf(&block, "  %s:\n    - name: trace-%s\n      matcher: \"*\"\n      command: %s\n", name, strings.ToLower(name), command)
	}
	block.WriteString(endMarker)
	current, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading Poolside settings: %w", err)
	}
	missing := os.IsNotExist(err)
	if missing && remove {
		return nil
	}
	text := string(current)
	legacy := strings.HasPrefix(text, legacyMarker) && isManagedBlock(beginMarker+strings.TrimPrefix(text, legacyMarker)+endMarker, block.String())
	if legacy {
		if remove {
			return os.Remove(p)
		}
		text = ""
	}
	start := strings.Index(text, beginMarker)
	end := strings.Index(text, endMarker)
	if (start >= 0) != (end >= 0) || (start >= 0 && (end < start || strings.Count(text, beginMarker) != 1 || strings.Count(text, endMarker) != 1)) {
		return fmt.Errorf("invalid Trace hooks block in Poolside settings")
	}
	if start >= 0 {
		end += len(endMarker)
		if !isManagedBlock(text[start:end], block.String()) {
			return fmt.Errorf("trace hooks in Poolside settings were modified; preserve and merge them manually")
		}
		if !remove {
			if text[start:end] == block.String() {
				return nil
			}
			return os.WriteFile(p, []byte(text[:start]+block.String()+text[end:]), 0600)
		}
		if start > 0 && text[start-1] == '\n' {
			start--
		}
		text = text[:start] + text[end:]
		if text == "" {
			return os.Remove(p)
		}
		return os.WriteFile(p, []byte(text), 0600)
	}
	merged, handled, err := mergeExistingHooks(text, remove)
	if err != nil {
		return err
	}
	if handled {
		if merged == text {
			return nil
		}
		return os.WriteFile(p, []byte(merged), 0600)
	}
	if remove {
		return nil
	}
	if !canAppendHooks(text) {
		return fmt.Errorf("poolside settings at %s contain hooks or unsupported YAML; merge the Trace hooks manually", p)
	}
	if text != "" {
		text += "\n"
	}
	text += block.String()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating Poolside config directory: %w", err)
	}
	if err := os.WriteFile(p, []byte(text), 0600); err != nil {
		return fmt.Errorf("writing Poolside settings: %w", err)
	}
	return nil
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p struct {
		SessionID string `json:"session_id"`
		HookName  string `json:"hook_event_name"`
		CWD       string `json:"cwd"`
		Prompt    string `json:"prompt"`
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Path string `json:"path"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Poolside hook: %w", err)
	}
	if p.SessionID == "" || (p.HookName != "" && p.HookName != name) {
		return nil, nil
	}
	e := event.Event{Agent: "poolside", AgentSessionID: p.SessionID, Worktree: root, At: time.Now().UnixMilli()}
	switch name {
	case "SessionStart":
		e.Kind = event.SessionStart
	case "UserPromptSubmit":
		e.Kind, e.Prompt = event.TurnStart, p.Prompt
	case "Stop":
		scan := e
		scan.Kind = event.ToolEdit
		e.Kind = event.TurnEnd
		return []event.Event{scan.Sanitized(), e.Sanitized()}, nil
	case "PostToolUse":
		switch p.ToolName {
		case "edit", "write", "remove", "shell":
			e.Kind = event.ToolEdit
		case "read":
			e.Kind = event.FileRead
		default:
			return nil, nil
		}
		e.ToolName = p.ToolName
		if p.ToolInput.Path != "" {
			path := p.ToolInput.Path
			if !filepath.IsAbs(path) && filepath.IsAbs(p.CWD) {
				path = filepath.Join(p.CWD, path)
			}
			e.Paths = []string{path}
		}
	default:
		return nil, nil
	}
	return []event.Event{e.Sanitized()}, nil
}
