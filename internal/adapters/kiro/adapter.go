package kiro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/codexhook"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

const relative = ".kiro/hooks/trace.json"
const marker = "Trace managed hook"

var hookNames = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"}

func managedHooks(b []byte, agent string) bool {
	var top map[string]any
	if json.Unmarshal(b, &top) != nil || len(top) != 2 || top["version"] != "v1" {
		return false
	}
	hooks, ok := top["hooks"].([]any)
	if !ok || (len(hooks) != len(hookNames) && len(hooks) != len(hookNames)+1) {
		return false
	}
	for i, value := range hooks {
		name := "SessionEnd" // Older Trace versions installed this unsupported V3 trigger.
		if i < len(hookNames) {
			name = hookNames[i]
		}
		hook, ok := value.(map[string]any)
		if !ok || len(hook) != 3 || hook["name"] != marker+" "+name || hook["trigger"] != name {
			return false
		}
		action, ok := hook["action"].(map[string]any)
		if !ok || len(action) != 2 || action["type"] != "command" {
			return false
		}
		command, ok := action["command"].(string)
		if !ok || !hookcmd.IsTraceHook(command, agent) || !hookcmd.IsTraceHookFor(command, agent, name) {
			return false
		}
	}
	return true
}

func Install(root string) error {
	return install(root, "kiro")
}

func InstallGlobal(home string) error {
	return install(home, "kiro-global")
}

func install(root, agent string) error {
	p := filepath.Join(root, relative)
	info, err := os.Lstat(p)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular Kiro hooks at %s", p)
		}
		old, err := os.ReadFile(p)
		if err != nil || (!managedHooks(old, "kiro") && !managedHooks(old, "kiro-global")) {
			return fmt.Errorf("refusing to replace modified Kiro hooks at %s", p)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking Kiro hooks: %w", err)
	}
	var hooks []any
	for _, name := range hookNames {
		hooks = append(hooks, map[string]any{"name": marker + " " + name, "trigger": name, "action": map[string]string{"type": "command", "command": hookcmd.Command(agent, name)}})
	}
	b, err := json.MarshalIndent(map[string]any{"version": "v1", "hooks": hooks}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding Kiro hooks: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating Kiro hooks directory: %w", err)
	}
	if err := os.WriteFile(p, append(b, '\n'), 0600); err != nil {
		return fmt.Errorf("writing Kiro hooks: %w", err)
	}
	return nil
}

func Uninstall(root string) error {
	p := filepath.Join(root, relative)
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to remove non-regular Kiro hooks at %s", p)
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading Kiro hooks: %w", err)
	}
	if managedHooks(b, "kiro") || managedHooks(b, "kiro-global") {
		return os.Remove(p)
	}
	return fmt.Errorf("refusing to remove modified Kiro hooks at %s", p)
}

// HasProjectHook only suppresses a global hook for an unchanged Trace project
// configuration. User-modified or unrelated hook files are not treated as ours.
func HasProjectHook(root, name string) bool {
	for _, eventName := range hookNames {
		if eventName == name {
			b, err := os.ReadFile(filepath.Join(root, relative))
			return err == nil && managedHooks(b, "kiro")
		}
	}
	return false
}

func Parse(name string, body []byte, root, prompt string) ([]event.Event, error) {
	var envelope struct {
		SessionID string `json:"session_id"`
		HookName  string `json:"hook_event_name"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parsing Kiro hook: %w", err)
	}
	if envelope.SessionID == "" || (envelope.HookName != "" && !strings.EqualFold(envelope.HookName, name)) {
		return nil, nil
	}
	if name == "PostToolUse" {
		var p struct {
			SessionID string `json:"session_id"`
			CWD       string `json:"cwd"`
			ToolName  string `json:"tool_name"`
			ToolInput struct {
				Path     string `json:"path"`
				FilePath string `json:"file_path"`
			} `json:"tool_input"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("parsing Kiro tool hook: %w", err)
		}
		var kind event.Kind
		switch p.ToolName {
		case "fs_write", "execute_bash", "execute_cmd":
			kind = event.ToolEdit
		case "fs_read":
			kind = event.FileRead
		}
		if kind != "" {
			ev := event.Event{Kind: kind, Agent: "kiro", AgentSessionID: p.SessionID, Worktree: root, At: time.Now().UnixMilli(), ToolName: p.ToolName}
			path := p.ToolInput.FilePath
			if path == "" {
				path = p.ToolInput.Path
			}
			if path != "" {
				if !filepath.IsAbs(path) && filepath.IsAbs(p.CWD) {
					path = filepath.Join(p.CWD, path)
				}
				ev.Paths = []string{path}
			}
			return []event.Event{ev.Sanitized()}, nil
		}
	}
	events, err := codexhook.Parse(name, body, root)
	if err != nil {
		return nil, err
	}
	for i := range events {
		events[i].Agent = "kiro"
		if events[i].Kind == event.TurnStart && events[i].Prompt == "" {
			events[i].Prompt = promptFallback(prompt)
			events[i] = events[i].Sanitized()
		}
	}
	return events, nil
}

// USER_PROMPT is plain text in some versions, while Kiro IDE can also use it
// for a structured tool-event envelope. Do not record that envelope as a
// user's prompt when the actual prompt is unavailable.
func promptFallback(value string) string {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &object) != nil || object == nil {
		return value
	}
	for _, key := range []string{"prompt", "userPrompt"} {
		var prompt string
		if json.Unmarshal(object[key], &prompt) == nil && prompt != "" {
			return prompt
		}
	}
	for _, key := range []string{"toolName", "toolArgs", "toolResult", "toolSuccess"} {
		if _, ok := object[key]; ok {
			return ""
		}
	}
	return value
}
