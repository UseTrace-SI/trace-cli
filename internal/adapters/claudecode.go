package adapters

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/claudecode"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/copilot"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookconfig"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/userstate"
)

// claudeGlobalEvents are the events `trace setup` installs for Claude Code.
var claudeGlobalEvents = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "StopFailure", "SessionEnd"}

type claudeCode struct {
	Hooks
	Global
}

func (claudeCode) Name() string { return "claude-code" }

func (claudeCode) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Claude Code", Verification: Verified}
}

func (claudeCode) ResolveHookRoot(c *HookCall) (string, error) {
	_, root, err := payloadCWD(c)
	return root, err
}

// DecodeHook: Copilot CLI also runs a repository's .claude/settings*.json
// hooks. Its payloads (see copilot.IsClaudeHookPayload) are recorded as
// Copilot sessions; every Claude Code payload is parsed as before.
func (claudeCode) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	if copilot.IsClaudeHookPayload(body) {
		return copilot.ParseClaudeHook(c.Event, body, root)
	}
	return claudecode.ParseEvents(c.Event, body, root)
}

// SkipHook drops a Copilot call through the Claude Code hooks when Copilot
// is disabled or Trace's own Copilot hook (project or user level) records
// the same event, so a session is never recorded twice.
func (claudeCode) SkipHook(c *HookCall, root string) bool {
	body, err := c.Body()
	if err != nil || !copilot.IsClaudeHookPayload(body) {
		return false
	}
	name, ok := copilot.ClaudeHookEvent(c.Event)
	if !ok || userstate.DisabledFor("copilot") || copilot.HasProjectHook(root, name) {
		return true
	}
	home, err := os.UserHomeDir()
	return err == nil && copilot.HasGlobalHook(home, name)
}

func (claudeCode) InvalidPayloadMessage() string { return "trace hook: invalid Claude payload" }

func (claudeCode) InstallProject(root string) error { return claudecode.Install(root) }
func (claudeCode) RemoveProject(root string) error  { return claudecode.Uninstall(root) }

func (claudeCode) Integration() Integration {
	return Integration{Kind: "CLI", Commands: []string{"claude"}, Profiles: []string{".claude"}}
}

func (claudeCode) SetupNeedsStateDir() bool { return true }

// SetupGlobal honors an absolute CLAUDE_CONFIG_DIR.
func (a claudeCode) SetupGlobal(home string, remove bool) error {
	root, relative := home, ".claude/settings.json"
	if config := os.Getenv("CLAUDE_CONFIG_DIR"); config != "" {
		if !filepath.IsAbs(config) {
			return fmt.Errorf("CLAUDE_CONFIG_DIR must be absolute")
		}
		root, relative = config, "settings.json"
	}
	if remove {
		return hookconfig.Uninstall(root, a.Name(), relative, true)
	}
	return hookconfig.Install(root, a.Name(), relative, claudeGlobalEvents, true)
}

func (claudeCode) LaunchCommand(context string) (string, []string) {
	return "claude", []string{context}
}

func (claudeCode) ReviewCommand() (string, []string) {
	return "claude", []string{"--print", "--output-format", "text", "--tools", "", "--strict-mcp-config", "--no-session-persistence"}
}

// TranscriptStart: the turn's part of the transcript begins at its size
// when the turn starts.
func (claudeCode) TranscriptStart(path string) (string, int64) {
	if info, err := os.Stat(path); err == nil {
		return "", info.Size()
	}
	return "", 0
}

func (claudeCode) ReadTranscript(path string, offset int64, root string) (TranscriptTurn, int64) {
	context, next, err := claudecode.ReadTranscript(path, offset, root)
	if err != nil {
		return TranscriptTurn{}, offset
	}
	return TranscriptTurn{Reasoning: context.Reasoning, ToolCalls: context.ToolCalls, FilesRead: context.FilesRead, TokensIn: context.TokensIn, TokensOut: context.TokensOut}, next
}

// ResponseInReasoning: the Stop hook carries the final assistant message.
func (claudeCode) ResponseInReasoning() bool { return true }

func (claudeCode) InstallEvidence() []InstallEvidence {
	return []InstallEvidence{{Path: ".claude/settings.local.json", Contains: " hook claude-code Stop"}}
}
