package kimi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/claudecode"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

const start = "# BEGIN TRACE KIMI HOOKS"
const end = "# END TRACE KIMI HOOKS"

var hookNames = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "StopFailure", "Interrupt", "SessionEnd"}

func managedBlock(block string) bool {
	lines := strings.Split(block, "\n")
	if len(lines) != 2+4*len(hookNames) || lines[0] != start || lines[len(lines)-1] != end {
		return false
	}
	for i, name := range hookNames {
		at := 1 + i*4
		if lines[at] != "" || lines[at+1] != "[[hooks]]" || lines[at+2] != "event = "+strconv.Quote(name) || !strings.HasPrefix(lines[at+3], "command = ") {
			return false
		}
		var command string
		if json.Unmarshal([]byte(strings.TrimPrefix(lines[at+3], "command = ")), &command) != nil || !hookcmd.IsTraceHook(command, "kimi") || !hookcmd.IsTraceHookFor(command, "kimi", name) {
			return false
		}
	}
	return true
}

func configPath(home string) (string, error) {
	if custom := os.Getenv("KIMI_CODE_HOME"); custom != "" {
		if !filepath.IsAbs(custom) {
			return "", fmt.Errorf("KIMI_CODE_HOME must be absolute; relative paths depend on the Kimi launch directory")
		}
		return filepath.Join(custom, "config.toml"), nil
	}
	return filepath.Join(home, ".kimi-code", "config.toml"), nil
}

func rewrite(original string, install bool) (string, error) {
	begin := strings.Index(original, start)
	finish := strings.Index(original, end)
	if (begin >= 0) != (finish >= 0) || (begin >= 0 && (finish < begin || strings.Count(original, start) != 1 || strings.Count(original, end) != 1)) {
		return "", fmt.Errorf("invalid Trace hooks block in Kimi config")
	}
	if begin >= 0 {
		finish += len(end)
		if !managedBlock(original[begin:finish]) {
			return "", fmt.Errorf("Trace hooks in Kimi config were modified; preserve and merge them manually")
		}
		if finish < len(original) && original[finish] == '\n' {
			finish++
		}
		original = original[:begin] + original[finish:]
	}
	if !install {
		return original, nil
	}
	var block strings.Builder
	if original != "" && !strings.HasSuffix(original, "\n") {
		block.WriteByte('\n')
	}
	block.WriteString(start + "\n")
	for _, name := range hookNames {
		command, err := json.Marshal(hookcmd.Command("kimi", name))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&block, "\n[[hooks]]\nevent = %q\ncommand = %s\n", name, command)
	}
	block.WriteString(end + "\n")
	return original + block.String(), nil
}

func Install(home string) error {
	p, err := configPath(home)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading Kimi config: %w", err)
	}
	updated, err := rewrite(string(b), true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating Kimi config directory: %w", err)
	}
	if err := os.WriteFile(p, []byte(updated), 0600); err != nil {
		return fmt.Errorf("writing Kimi config: %w", err)
	}
	return nil
}

func Uninstall(home string) error {
	p, err := configPath(home)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading Kimi config: %w", err)
	}
	updated, err := rewrite(string(b), false)
	if err != nil {
		return err
	}
	if updated == string(b) {
		return nil
	}
	if updated == "" {
		return os.Remove(p)
	}
	return os.WriteFile(p, []byte(updated), 0600)
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var envelope struct {
		SessionID string `json:"session_id"`
		HookName  string `json:"hook_event_name"`
		AgentID   string `json:"agent_id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parsing Kimi hook: %w", err)
	}
	if envelope.SessionID == "" || (envelope.HookName != "" && envelope.HookName != name) {
		return nil, nil
	}
	if envelope.AgentID != "" && name != "PostToolUse" {
		return nil, nil
	}
	if name == "UserPromptSubmit" {
		var p struct {
			TranscriptPath string          `json:"transcript_path"`
			Prompt         json.RawMessage `json:"prompt"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("parsing Kimi prompt hook: %w", err)
		}
		var prompt string
		if json.Unmarshal(p.Prompt, &prompt) != nil {
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(p.Prompt, &blocks); err != nil {
				return nil, fmt.Errorf("parsing Kimi prompt content: %w", err)
			}
			var text []string
			for _, block := range blocks {
				if block.Type == "text" && block.Text != "" {
					text = append(text, block.Text)
				}
			}
			prompt = strings.Join(text, "\n")
		}
		ev := event.Event{Kind: event.TurnStart, Agent: "kimi", AgentSessionID: envelope.SessionID, Worktree: root, At: time.Now().UnixMilli(), Prompt: prompt, TranscriptPath: p.TranscriptPath}
		return []event.Event{ev.Sanitized()}, nil
	}
	if name == "PostToolUse" {
		var p struct {
			SessionID string `json:"session_id"`
			CWD       string `json:"cwd"`
			ToolName  string `json:"tool_name"`
			ToolInput struct {
				FilePath string `json:"file_path"`
				Path     string `json:"path"`
			} `json:"tool_input"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("parsing Kimi tool hook: %w", err)
		}
		var kind event.Kind
		switch p.ToolName {
		case "WriteFile", "StrReplaceFile", "Shell", "Write", "Edit", "Bash":
			kind = event.ToolEdit
		case "ReadFile", "Read", "ReadMediaFile":
			kind = event.FileRead
		default:
			return nil, nil
		}
		ev := event.Event{Kind: kind, Agent: "kimi", AgentSessionID: p.SessionID, Worktree: root, ToolName: p.ToolName, At: time.Now().UnixMilli()}
		path := p.ToolInput.Path
		if path == "" {
			path = p.ToolInput.FilePath
		}
		if path != "" {
			if !filepath.IsAbs(path) && filepath.IsAbs(p.CWD) {
				path = filepath.Join(p.CWD, path)
			}
			ev.Paths = []string{path}
		}
		return []event.Event{ev.Sanitized()}, nil
	}
	if name == "StopFailure" || name == "Interrupt" {
		var p struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("parsing Kimi stop hook: %w", err)
		}
		scan := event.Event{Kind: event.ToolEdit, Agent: "kimi", AgentSessionID: p.SessionID, Worktree: root, At: time.Now().UnixMilli()}
		end := scan
		end.Kind = event.TurnEnd
		return []event.Event{scan.Sanitized(), end.Sanitized()}, nil
	}
	events, err := claudecode.Parse(name, body, root)
	if err != nil {
		return nil, err
	}
	for i := range events {
		events[i].Agent = "kimi"
	}
	if len(events) == 1 && (name == "Stop" || name == "SessionEnd") {
		ev := events[0]
		scan := ev
		scan.Kind, scan.Text = event.ToolEdit, ""
		result := []event.Event{scan.Sanitized()}
		if name == "Stop" && ev.Text != "" {
			response := ev
			response.Kind, response.ContextType = event.TurnContext, "response"
			response.Text = textutil.TruncateBytes(ev.Text, 64<<10)
			result = append(result, response.Sanitized())
		}
		if name == "SessionEnd" {
			turnEnd := ev
			turnEnd.Kind = event.TurnEnd
			result = append(result, turnEnd.Sanitized())
		}
		ev.Text = ""
		return append(result, ev.Sanitized()), nil
	}
	return events, nil
}
