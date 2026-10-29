package cline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

const marker = "# Trace Cline CLI hook. Managed by trace agent add/remove."

var names = []string{"TaskStart", "TaskResume", "UserPromptSubmit", "PostToolUse", "TaskComplete", "TaskError", "TaskCancel", "SessionShutdown"}

func path(root, name string) string { return filepath.Join(root, ".cline", "hooks", name) }

func Install(root string) error {
	return install(filepath.Join(root, ".cline", "hooks"), root)
}

func globalDir(home string) (string, error) {
	dir := strings.TrimSpace(os.Getenv("CLINE_DIR"))
	if dir == "" {
		dir = filepath.Join(home, ".cline")
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("Cline config directory must be absolute")
	}
	return filepath.Join(dir, "hooks"), nil
}

func InstallGlobal(home string) error {
	dir, err := globalDir(home)
	if err != nil {
		return err
	}
	return install(dir, "")
}

func UninstallGlobal(home string) error {
	dir, err := globalDir(home)
	if err != nil {
		return err
	}
	return uninstall(dir)
}

func HasProjectHook(root, name string) bool {
	p := path(root, name)
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	installedRoot, ok := managedHookRoot(b, name)
	return ok && installedRoot != "" && filepath.Clean(installedRoot) == filepath.Clean(root)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func hookBody(binary, root, name string) string {
	command := shellQuote(binary)
	if root != "" {
		command += " --repo " + shellQuote(root)
	}
	command += " hook cline " + name
	return "#!/bin/sh\n" + marker + "\n" + command + " >/dev/null 2>&1\nprintf '%s\\n' '{\"cancel\":false}'\n"
}

// readQuoted accepts only the shell quoting emitted by shellQuote.
func readQuoted(s string) (string, string, bool) {
	if !strings.HasPrefix(s, "'") {
		return "", "", false
	}
	s = s[1:]
	var value strings.Builder
	for {
		i := strings.IndexByte(s, '\'')
		if i < 0 {
			return "", "", false
		}
		value.WriteString(s[:i])
		s = s[i:]
		if strings.HasPrefix(s, `'\''`) {
			value.WriteByte('\'')
			s = s[4:]
			continue
		}
		return value.String(), s[1:], true
	}
}

func managedHook(data []byte, name string) bool {
	_, ok := managedHookRoot(data, name)
	return ok
}

func managedHookRoot(data []byte, name string) (string, bool) {
	text := string(data)
	prefix := "#!/bin/sh\n" + marker + "\n"
	if !strings.HasPrefix(text, prefix) {
		return "", false
	}
	binary, rest, ok := readQuoted(strings.TrimPrefix(text, prefix))
	if !ok || !filepath.IsAbs(binary) {
		return "", false
	}
	root := ""
	if strings.HasPrefix(rest, " --repo ") {
		root, rest, ok = readQuoted(strings.TrimPrefix(rest, " --repo "))
		if !ok || !filepath.IsAbs(root) {
			return "", false
		}
	}
	return root, text == hookBody(binary, root, name)
}

func install(dir, root string) error {
	for _, name := range names {
		p := filepath.Join(dir, name)
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular Cline hook at %s", p)
		}
		b, err := os.ReadFile(p)
		if err != nil || !managedHook(b, name) {
			return fmt.Errorf("refusing to replace modified Cline hook at %s", p)
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating Cline hooks directory: %w", err)
	}
	for _, name := range names {
		body := hookBody(hookcmd.Path(), root, name)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
			return fmt.Errorf("writing Cline hook: %w", err)
		}
	}
	return nil
}

func Uninstall(root string) error {
	return uninstall(filepath.Join(root, ".cline", "hooks"))
}

func uninstall(dir string) error {
	var remove []string
	for _, name := range names {
		p := filepath.Join(dir, name)
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to remove non-regular Cline hook at %s", p)
		}
		b, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading Cline hook: %w", err)
		}
		if managedHook(b, name) {
			remove = append(remove, p)
		} else {
			return fmt.Errorf("refusing to remove modified Cline hook at %s", p)
		}
	}
	for _, p := range remove {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing Cline hook: %w", err)
		}
	}
	return nil
}

// readPaths accepts Cline's documented file request forms without retaining
// other tool arguments. The bound limits work done in a synchronous hook.
func readPaths(raw json.RawMessage, depth int, paths *[]string) {
	if depth > 3 || len(*paths) >= 256 || len(raw) == 0 {
		return
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		if value != "" && !strings.Contains(value, "://") {
			*paths = append(*paths, value)
		}
		return
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		for _, item := range list {
			readPaths(item, depth+1, paths)
			if len(*paths) >= 256 {
				break
			}
		}
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return
	}
	for _, key := range []string{"path", "file_path", "filePath", "files", "paths", "file_paths"} {
		readPaths(fields[key], depth+1, paths)
	}
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p struct {
		TaskID         string   `json:"taskId"`
		ParentAgentID  *string  `json:"parent_agent_id"`
		WorkspaceRoots []string `json:"workspaceRoots"`
		TaskStart      struct {
			TaskMetadata struct {
				InitialTask string `json:"initialTask"`
			} `json:"taskMetadata"`
		} `json:"taskStart"`
		Turn struct {
			OutputText string `json:"outputText"`
		} `json:"turn"`
		Prompt struct {
			Prompt string `json:"prompt"`
		} `json:"userPromptSubmit"`
		PostTool struct {
			ToolName   string          `json:"toolName"`
			Success    bool            `json:"success"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"postToolUse"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Cline hook: %w", err)
	}
	if p.TaskID == "" || (p.ParentAgentID != nil && *p.ParentAgentID != "") {
		return nil, nil
	}
	ev := event.Event{Agent: "cline", AgentSessionID: p.TaskID, Worktree: root, At: time.Now().UnixMilli()}
	switch name {
	case "TaskStart":
		ev.Kind = event.SessionStart
		if p.TaskStart.TaskMetadata.InitialTask != "" {
			turn := ev
			turn.Kind, turn.Prompt = event.TurnStart, p.TaskStart.TaskMetadata.InitialTask
			turn.InitialTurnOnly = true
			return []event.Event{ev.Sanitized(), turn.Sanitized()}, nil
		}
	case "TaskResume":
		ev.Kind = event.SessionStart
	case "UserPromptSubmit":
		ev.Kind, ev.Prompt = event.TurnStart, p.Prompt.Prompt
	case "TaskComplete", "TaskError", "TaskCancel":
		scan := ev
		scan.Kind = event.ToolEdit
		result := []event.Event{scan.Sanitized()}
		if name == "TaskComplete" && p.Turn.OutputText != "" {
			response := ev
			response.Kind, response.ContextType = event.TurnContext, "response"
			response.Text = textutil.TruncateBytes(p.Turn.OutputText, 64<<10)
			result = append(result, response.Sanitized())
		}
		ev.Kind = event.TurnEnd
		return append(result, ev.Sanitized()), nil
	case "SessionShutdown":
		scan := ev
		scan.Kind = event.ToolEdit
		end := ev
		end.Kind = event.TurnEnd
		ev.Kind = event.SessionEnd
		return []event.Event{scan.Sanitized(), end.Sanitized(), ev.Sanitized()}, nil
	case "PostToolUse":
		switch p.PostTool.ToolName {
		case "write_to_file", "replace_in_file", "apply_patch", "editor", "run_commands", "execute_command", "bash":
			ev.Kind = event.ToolEdit
		case "read_file", "read_files":
			ev.Kind = event.FileRead
		default:
			return nil, nil
		}
		ev.ToolName = p.PostTool.ToolName
		if !p.PostTool.Success {
			if ev.Kind == event.FileRead {
				return nil, nil
			}
			// A failing command or patch may already have changed files. Request
			// a full scan instead of asserting that a named path was edited.
			return []event.Event{ev.Sanitized()}, nil
		}
		var paths []string
		var params map[string]json.RawMessage
		_ = json.Unmarshal(p.PostTool.Parameters, &params)
		if p.PostTool.ToolName == "read_files" {
			readPaths(p.PostTool.Parameters, 0, &paths)
		} else {
			readPaths(params["path"], 0, &paths)
		}
		seen := make(map[string]bool)
		for _, f := range paths {
			path := ""
			if filepath.IsAbs(f) {
				path = filepath.Clean(f)
			} else if len(p.WorkspaceRoots) == 0 {
				path = filepath.Clean(f)
			} else if len(p.WorkspaceRoots) == 1 && filepath.IsAbs(p.WorkspaceRoots[0]) {
				path = filepath.Join(p.WorkspaceRoots[0], f)
			}
			if path != "" && !seen[path] {
				ev.Paths = append(ev.Paths, path)
				seen[path] = true
			}
		}
	default:
		return nil, nil
	}
	return []event.Event{ev.Sanitized()}, nil
}
