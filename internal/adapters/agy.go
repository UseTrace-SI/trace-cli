package adapters

import (
	"github.com/UseTrace-SI/trace-cli/internal/adapters/agy"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

type agyAdapter struct{ Hooks }

func (agyAdapter) Name() string { return "agy" }

func (agyAdapter) Capabilities() Capabilities {
	return Capabilities{DisplayName: "Antigravity CLI", Verification: Preview}
}

// HookResponse: Antigravity waits for a reply to every hook and an explicit
// decision on Stop.
func (agyAdapter) HookResponse(hookEvent string) (string, bool) {
	if hookEvent == "Stop" {
		return `{"decision":"allow"}`, true
	}
	return "{}", true
}

func (agyAdapter) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	return agy.Parse(c.Event, body, root)
}

func (agyAdapter) InvalidPayloadMessage() string { return "trace hook: invalid Antigravity payload" }

func (agyAdapter) InstallProject(root string) error { return agy.Install(root) }
func (agyAdapter) RemoveProject(root string) error  { return agy.Uninstall(root) }

func (agyAdapter) LaunchCommand(context string) (string, []string) {
	return "agy", []string{"--prompt-interactive", context}
}

// TranscriptStart: the transcript's latest user request is the prompt, and
// the turn begins after it.
func (agyAdapter) TranscriptStart(path string) (string, int64) {
	latest, offset, err := agy.LatestPrompt(path)
	if err != nil {
		return "", 0
	}
	return latest, offset
}

// ReadTranscript also recovers the prompt from the latest user request when
// the turn's part of the transcript has none.
func (agyAdapter) ReadTranscript(path string, offset int64, root string) (TranscriptTurn, int64) {
	var turn TranscriptTurn
	next := offset
	if context, n, err := agy.ReadTranscript(path, offset, root); err == nil {
		turn = TranscriptTurn{Prompt: context.Prompt, Reasoning: context.Reasoning, ToolCalls: context.ToolCalls, FilesRead: context.FilesRead, WrittenPaths: context.WrittenPaths, HasCommand: context.HasCommand}
		next = n
	}
	if turn.Prompt == "" {
		turn.Prompt, _, _ = agy.LatestPrompt(path)
	}
	return turn, next
}

func (agyAdapter) ResponseInReasoning() bool { return false }

func (agyAdapter) InstallEvidence() []InstallEvidence {
	return []InstallEvidence{{Path: ".agents/hooks.json", Contains: " hook agy"}}
}
