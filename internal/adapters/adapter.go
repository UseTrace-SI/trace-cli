// Package adapters is the registry of coding-agent integrations. Each agent
// is one file in this package that implements Adapter and the capability
// interfaces it supports, plus one entry in registry.go; the CLI, daemon
// and engine iterate the registry instead of naming agents. The
// agent-specific parsers and installers stay in the subpackages
// (internal/adapters/<agent>). docs/adding-an-agent.md lists the steps.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// Verification records how far an integration has been checked against the
// real agent; see the README's integration table.
type Verification string

const (
	// Verified integrations passed live capture with the real agent.
	Verified Verification = "verified"
	// Preview integrations are covered by offline tests only.
	Preview Verification = "preview"
)

// Capabilities is documentation metadata about an agent. No command prints
// it, so it can never change CLI output; TestVerifiedAgentsMatchReadme
// keeps the README's integration table consistent with it, and
// docs/adding-an-agent.md asks new adapters to fill it in.
type Capabilities struct {
	// DisplayName is the agent's product name.
	DisplayName string
	// Verification is the live-coverage status of the integration.
	Verification Verification
}

// Adapter is one agent integration. Every adapter receives hook callbacks;
// ProjectInstaller, MarkerEnabled, GlobalInstaller, Launcher and Reviewer
// are optional capabilities.
type Adapter interface {
	// Name is the canonical name used in commands, events and settings.
	Name() string
	Capabilities() Capabilities

	// HookAliases are other names that agent configuration invokes as
	// `trace hook <alias> <event>`, all user-level ("-global") hooks.
	HookAliases() []string
	// ResolveHookRoot finds the worktree a hook callback belongs to when
	// the installed command does not pass --repo. An empty root without an
	// error means the process working directory's repository. An error
	// drops the callback, for example when a project hook of the same
	// agent will report it instead.
	ResolveHookRoot(c *HookCall) (string, error)
	// SkipHook drops a callback after its root is known.
	SkipHook(c *HookCall, root string) bool
	// StartsDaemon reports agent-specific events that begin a session or
	// turn, besides the common ones, so the hook restarts a stopped daemon.
	StartsDaemon(hookEvent string) bool
	// DecodeHook converts the callback's payload into Trace events.
	DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error)
	// InvalidPayloadMessage is printed to stderr when DecodeHook fails;
	// empty means fail silently.
	InvalidPayloadMessage() string
	// FramePerEvent sends each decoded event as its own frame instead of
	// one batch per callback.
	FramePerEvent() bool
	// HookResponse is what the agent expects on stdout for an event. It is
	// printed even when the callback is dropped, paused or fails.
	HookResponse(hookEvent string) (string, bool)
}

// ProjectInstaller merges an agent's hooks into one repository's
// configuration (`trace init --agent`, `trace agent add/remove`, `trace
// uninstall`).
type ProjectInstaller interface {
	Adapter
	InstallProject(root string) error
	RemoveProject(root string) error
}

// ProjectChecker is a ProjectInstaller that `trace init` checks before it
// changes anything.
type ProjectChecker interface {
	CheckProject(root string) error
}

// ProjectNoticer is an agent that needs user action after project install.
type ProjectNoticer interface {
	// ProjectNotice returns the agent's display label for `trace init` and
	// the additional setup instruction.
	ProjectNotice() (label, notice string)
}

// Marker is a file in .trace that enables an agent whose sessions the
// daemon reads from transcripts rather than project hooks.
type Marker struct {
	File  string
	Label string
	// RemovedByUninstall reports whether `trace uninstall` deletes it.
	RemovedByUninstall bool
}

// MarkerEnabled is an agent enabled per repository by a Marker.
type MarkerEnabled interface {
	Adapter
	EnableMarker() Marker
}

// Integration describes how to detect an installed agent.
type Integration struct {
	Kind     string
	Commands []string
	Profiles []string
	Apps     []string
}

// Detected reports whether the agent appears installed for the user.
func (item Integration) Detected(home string) bool {
	for _, name := range item.Commands {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	for _, profile := range item.Profiles {
		matches, _ := filepath.Glob(filepath.Join(home, profile))
		if len(matches) > 0 {
			return true
		}
	}
	for _, app := range item.Apps {
		for _, directory := range []string{"/Applications", filepath.Join(home, "Applications")} {
			if _, err := os.Stat(filepath.Join(directory, app)); err == nil {
				return true
			}
		}
	}
	return false
}

// GlobalInstaller installs user-level hooks (`trace setup`, `trace enable`).
type GlobalInstaller interface {
	Adapter
	Integration() Integration
	// SetupNeedsStateDir reports whether setup creates ~/.trace first.
	SetupNeedsStateDir() bool
	SetupGlobal(home string, remove bool) error
	// SetupJSONNotice is the activation step reported by `setup --json`;
	// a non-empty notice means the agent is not yet enabled. extra holds
	// additional result fields.
	SetupJSONNotice(home string, remove bool) (notice string, extra map[string]any)
	// SetupHeadline is the message when only this agent is set up, and
	// whether setting it up reports "Selected integrations installed.".
	SetupHeadline() (solo string, selected bool)
	// SetupNotice is appended to the text output of a successful setup.
	SetupNotice(home string) string
	// SetupRemovedNotice is appended to the text output of setup --remove.
	SetupRemovedNotice() string
}

// Launcher starts the agent interactively with a context prompt (fork,
// resume).
type Launcher interface {
	LaunchCommand(context string) (string, []string)
}

// Reviewer runs the agent headless as a read-only reviewer that reads the
// prompt from stdin.
type Reviewer interface {
	Adapter
	ReviewCommand() (string, []string)
}

// Runner runs one headless prompt with the agent (`trace run`).
type Runner interface {
	Adapter
	// RunCommand is the program, looked up on PATH, and its arguments.
	RunCommand(prompt string) (string, []string)
	// RunCapturesOutput reports whether `trace run` records the agent's
	// bounded output as turn context when neither output stream is a
	// terminal.
	RunCapturesOutput() bool
}

// SessionReader is a marker-enabled agent whose sessions the daemon reads
// from the agent's own files while the marker exists.
type SessionReader interface {
	MarkerEnabled
	// StartSessionReader is called once when the daemon starts. It
	// resolves what it needs, logs failures to logger and reads sessions in
	// its own goroutine until ctx ends, passing events to process.
	StartSessionReader(ctx context.Context, root string, logger *slog.Logger, process func(event.Event) error)
	// HooksSupersedeSessions reports whether an event kind this agent's
	// hooks already delivered for a session is dropped when the session
	// reader reports it too.
	HooksSupersedeSessions() bool
}

// TranscriptTurn is what an agent's transcript adds to a finished turn.
type TranscriptTurn struct {
	// Prompt recovers the user prompt when the hook did not record one.
	Prompt    string
	Reasoning string
	ToolCalls string
	FilesRead string
	// WrittenPaths are files the transcript shows the agent wrote; each
	// one without a tool edit in the turn is recorded as one.
	WrittenPaths []string
	// HasCommand records an unknown-path tool edit when the turn has none,
	// since a command may have changed any file.
	HasCommand bool
	TokensIn   int64
	TokensOut  int64
}

// TranscriptReader is an agent whose hook events carry a transcript path
// that the engine reads when a turn ends.
type TranscriptReader interface {
	Adapter
	// TranscriptStart is called when a turn starts: the prompt the
	// transcript records (empty keeps the hook's prompt) and the offset
	// at which the turn's part of the transcript begins.
	TranscriptStart(path string) (prompt string, offset int64)
	// ReadTranscript reads the turn from offset and returns the next
	// offset. The transcript is best effort: on failure it returns what it
	// recovered and offset unchanged.
	ReadTranscript(path string, offset int64, root string) (TranscriptTurn, int64)
	// ResponseInReasoning reports whether the final assistant text of the
	// turn-end event is appended to the reasoning when the transcript lacks
	// it.
	ResponseInReasoning() bool
}

// InstallEvidence is a file whose content shows that an agent's project
// integration is installed (`trace doctor`).
type InstallEvidence struct {
	// Path is relative to the repository root, slash-separated.
	Path     string
	Contains string
}

// ProjectEvidencer is an agent whose project integration `trace doctor`
// recognizes.
type ProjectEvidencer interface {
	InstallEvidence() []InstallEvidence
}

// HookCall is one `trace hook` invocation.
type HookCall struct {
	// Agent is the adapter's canonical name.
	Agent string
	// Invoked is the name the hook command used, possibly an alias.
	Invoked string
	Event   string
	// Global is true when an alias (a user-level hook) was invoked.
	Global bool

	stdin io.Reader
	body  []byte
	read  bool
}

// NewHookCall prepares a callback whose payload is read from stdin.
func NewHookCall(agent, invoked, hookEvent string, global bool, stdin io.Reader) *HookCall {
	return &HookCall{Agent: agent, Invoked: invoked, Event: hookEvent, Global: global, stdin: stdin}
}

// maxHookPayload bounds what a hook reads from the agent.
const maxHookPayload = 4 << 20

// Body reads the payload once; later calls return the same bytes.
func (c *HookCall) Body() ([]byte, error) {
	if c.read {
		return c.body, nil
	}
	b, err := io.ReadAll(io.LimitReader(c.stdin, maxHookPayload))
	if err != nil {
		return nil, err
	}
	c.body, c.read = b, true
	return b, nil
}

// Hooks provides the defaults of the hook methods: no aliases, the working
// directory's repository, the common daemon start events, silent failures,
// one batch per callback and no stdout response. Adapters embed it and
// override what differs.
type Hooks struct{}

func (Hooks) HookAliases() []string                     { return nil }
func (Hooks) ResolveHookRoot(*HookCall) (string, error) { return "", nil }
func (Hooks) SkipHook(*HookCall, string) bool           { return false }
func (Hooks) StartsDaemon(string) bool                  { return false }
func (Hooks) InvalidPayloadMessage() string             { return "" }
func (Hooks) FramePerEvent() bool                       { return false }
func (Hooks) HookResponse(string) (string, bool)        { return "", false }

// GenericHooks decodes the generic payload that Trace's own plugins,
// extensions and bridges send: the event argument is the event kind.
type GenericHooks struct{ Hooks }

func (GenericHooks) DecodeHook(c *HookCall, body []byte, root string) ([]event.Event, error) {
	ev, err := ParseGeneric(c.Agent, c.Event, body, root)
	if err != nil {
		return nil, err
	}
	return []event.Event{ev}, nil
}

func (GenericHooks) InvalidPayloadMessage() string { return "trace hook: invalid payload" }

// ParseGeneric decodes a generic hook payload.
func ParseGeneric(agent, hookEvent string, b []byte, root string) (event.Event, error) {
	var p map[string]json.RawMessage
	if len(b) > 0 {
		if err := json.Unmarshal(b, &p); err != nil {
			return event.Event{}, fmt.Errorf("parsing generic hook: %w", err)
		}
	}
	ev := event.Event{Kind: event.Kind(hookEvent), Agent: agent, Worktree: root, At: time.Now().UnixMilli()}
	str := func(key string) string { var v string; _ = json.Unmarshal(p[key], &v); return v }
	ev.AgentSessionID = str("session_id")
	ev.Prompt = str("prompt")
	ev.ToolName = str("tool_name")
	ev.ContextType = str("context_type")
	ev.Text = str("text")
	ev.TranscriptPath = str("transcript_path")
	ev.GitSHA = str("git_sha")
	_ = json.Unmarshal(p["paths"], &ev.Paths)
	_ = json.Unmarshal(p["at"], &ev.At)
	var input map[string]json.RawMessage
	_ = json.Unmarshal(p["tool_input"], &input)
	var filePath string
	_ = json.Unmarshal(input["file_path"], &filePath)
	if filePath != "" && len(ev.Paths) == 0 {
		ev.Paths = []string{filePath}
	}
	return ev.Sanitized(), nil
}

// Global provides the defaults of the GlobalInstaller notices.
type Global struct{}

func (Global) SetupNeedsStateDir() bool                              { return false }
func (Global) SetupJSONNotice(string, bool) (string, map[string]any) { return "", nil }
func (Global) SetupHeadline() (string, bool)                         { return "", false }
func (Global) SetupNotice(string) string                             { return "" }
func (Global) SetupRemovedNotice() string                            { return "" }

// genericAdapter handles hook callbacks for names no adapter registers.
type genericAdapter struct {
	GenericHooks
	name string
}

func (a genericAdapter) Name() string { return a.name }
func (genericAdapter) Capabilities() Capabilities {
	return Capabilities{Verification: Preview}
}
