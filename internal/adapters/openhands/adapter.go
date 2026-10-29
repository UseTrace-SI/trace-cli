package openhands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

var eventNames = [][2]string{{"SessionStart", "session_start"}, {"UserPromptSubmit", "user_prompt_submit"}, {"PostToolUse", "post_tool_use"}, {"Stop", "stop"}, {"SessionEnd", "session_end"}}

var errUnsafeProject = errors.New("unsafe OpenHands project hooks path")

// Setup installs global CLI hooks, preserving either supported config format.
func Setup(home string, remove bool) error {
	dir := os.Getenv("OH_PERSISTENCE_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".openhands")
	} else if dir == "~" {
		dir = home
	} else if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(home, dir[2:])
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("OH_PERSISTENCE_DIR must be absolute or use ~/; relative paths depend on the OpenHands launch directory")
	}
	return configure(dir, remove)
}

// InstallProject merges only an existing project file. Creating one would
// shadow the user's global OpenHands hooks for the entire project.
func InstallProject(root string) error {
	return project(root, false)
}

func UninstallProject(root string) error {
	return project(root, true)
}

// CheckProject reports whether a project file can be safely merged, without
// creating Trace state or changing OpenHands configuration.
func CheckProject(root string) error {
	_, err := projectDir(root)
	return err
}

func projectDir(root string) (string, error) {
	dir := filepath.Join(root, ".openhands")
	parent, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("OpenHands project hooks do not exist; use trace setup --agent openhands for global hooks: %w", err)
	}
	if err != nil {
		return "", fmt.Errorf("checking OpenHands project directory: %w", err)
	}
	if !parent.IsDir() {
		return "", fmt.Errorf("refusing to change linked or non-directory OpenHands project directory: %w", errUnsafeProject)
	}
	info, err := os.Lstat(filepath.Join(dir, "hooks.json"))
	if os.IsNotExist(err) {
		return "", fmt.Errorf("OpenHands project hooks do not exist; use trace setup --agent openhands for global hooks: %w", err)
	}
	if err != nil {
		return "", fmt.Errorf("checking OpenHands project hooks: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("refusing to change non-regular OpenHands project hooks: %w", errUnsafeProject)
	}
	return dir, nil
}

func project(root string, remove bool) error {
	dir, err := projectDir(root)
	if err != nil {
		if remove && (errors.Is(err, os.ErrNotExist) || errors.Is(err, errUnsafeProject)) {
			return nil
		}
		return err
	}
	wrapped := false
	if remove {
		data, err := os.ReadFile(filepath.Join(dir, "hooks.json"))
		if err != nil {
			return fmt.Errorf("reading OpenHands project hooks: %w", err)
		}
		var top map[string]json.RawMessage
		if json.Unmarshal(data, &top) == nil {
			_, wrapped = top["hooks"]
		}
	}
	if err := configure(dir, remove); err != nil {
		return err
	}
	if remove {
		// Keep an empty project file so global hooks remain shadowed.
		path := filepath.Join(dir, "hooks.json")
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			empty := "{}\n"
			if wrapped {
				empty = "{\"hooks\":{}}\n"
			}
			if err := os.WriteFile(path, []byte(empty), 0600); err != nil {
				return fmt.Errorf("preserving OpenHands project hooks: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("checking OpenHands project hooks: %w", err)
		} else if wrapped {
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading OpenHands project hooks: %w", err)
			}
			var top map[string]json.RawMessage
			if err := json.Unmarshal(data, &top); err != nil {
				return fmt.Errorf("parsing OpenHands project hooks: %w", err)
			}
			if _, exists := top["hooks"]; !exists {
				top["hooks"] = json.RawMessage(`{}`)
				data, err = json.MarshalIndent(top, "", "  ")
				if err != nil {
					return fmt.Errorf("encoding OpenHands project hooks: %w", err)
				}
				if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
					return fmt.Errorf("preserving OpenHands project hooks: %w", err)
				}
			}
		}
	}
	return nil
}

func configure(dir string, remove bool) error {
	p := filepath.Join(dir, "hooks.json")
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading OpenHands hooks: %w", err)
	}
	var top map[string]any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &top); err != nil || top == nil {
			return fmt.Errorf("invalid OpenHands hooks configuration")
		}
	} else {
		top = map[string]any{}
	}
	hooks := top
	wrapped := false
	if value, ok := top["hooks"]; ok {
		wrapped = true
		var valid bool
		hooks, valid = value.(map[string]any)
		if !valid {
			return fmt.Errorf("invalid OpenHands hooks wrapper")
		}
	}
	if remove {
		if wrapped {
			return hookconfig.Uninstall(dir, "openhands", "hooks.json", true)
		}
		return hookconfig.UninstallFlat(dir, "openhands", "hooks.json")
	}
	var names []string
	for _, pair := range eventNames {
		_, pascal := hooks[pair[0]]
		_, snake := hooks[pair[1]]
		if pascal && snake {
			return fmt.Errorf("duplicate OpenHands hook event %s", pair[0])
		}
		if snake {
			names = append(names, pair[1])
		} else {
			names = append(names, pair[0])
		}
	}
	if wrapped {
		return hookconfig.Install(dir, "openhands", "hooks.json", names, true)
	}
	return hookconfig.InstallFlat(dir, "openhands", "hooks.json", names)
}

func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p struct {
		SessionID  string `json:"session_id"`
		EventType  string `json:"event_type"`
		WorkingDir string `json:"working_dir"`
		Message    string `json:"message"`
		ToolName   string `json:"tool_name"`
		ToolInput  struct {
			Path    json.RawMessage `json:"path"`
			Command json.RawMessage `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing OpenHands hook: %w", err)
	}
	if p.SessionID == "" || (p.EventType != "" && !strings.EqualFold(strings.ReplaceAll(p.EventType, "_", ""), strings.ReplaceAll(name, "_", ""))) {
		return nil, nil
	}
	e := event.Event{Agent: "openhands", AgentSessionID: p.SessionID, Worktree: root, At: time.Now().UnixMilli()}
	switch strings.ToLower(strings.ReplaceAll(name, "_", "")) {
	case "sessionstart":
		e.Kind = event.SessionStart
	case "userpromptsubmit":
		e.Kind, e.Prompt = event.TurnStart, p.Message
	case "sessionend":
		scan := e
		scan.Kind = event.ToolEdit
		end := e
		end.Kind = event.TurnEnd
		e.Kind = event.SessionEnd
		return []event.Event{scan.Sanitized(), end.Sanitized(), e.Sanitized()}, nil
	case "stop":
		scan := e
		scan.Kind = event.ToolEdit
		e.Kind = event.TurnEnd
		return []event.Event{scan.Sanitized(), e.Sanitized()}, nil
	case "posttooluse":
		var command, path string
		_ = json.Unmarshal(p.ToolInput.Command, &command)
		_ = json.Unmarshal(p.ToolInput.Path, &path)
		switch p.ToolName {
		case "file_editor", "planning_file_editor":
			if command == "view" {
				e.Kind = event.FileRead
			} else {
				e.Kind = event.ToolEdit
			}
		case "terminal":
			e.Kind = event.ToolEdit
		default:
			return nil, nil
		}
		e.ToolName = p.ToolName
		if path != "" {
			if !filepath.IsAbs(path) && filepath.IsAbs(p.WorkingDir) {
				path = filepath.Join(p.WorkingDir, path)
			}
			e.Paths = []string{path}
		}
	default:
		return nil, nil
	}
	return []event.Event{e.Sanitized()}, nil
}
