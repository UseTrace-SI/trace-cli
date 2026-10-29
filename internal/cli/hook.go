package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/agy"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/auggie"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/claudecode"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/cline"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/codexhook"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/copilot"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/cursor"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/factory"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/gemini"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/goose"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/grok"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/hermes"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/kimi"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/kiro"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/openclaude"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/openhands"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/poolside"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/qwen"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/windsurf"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
	"github.com/spf13/cobra"
)

func hookCommand(repoPath *string) *cobra.Command {
	return &cobra.Command{Use: "hook <agent> <event>", Hidden: true, Run: func(_ *cobra.Command, args []string) {
		if len(args) != 2 {
			return
		}
		runHook(*repoPath, args[0], args[1])
	}}
}

// TryHook handles agent events before Cobra constructs the rest of the CLI.
// It returns false for non-hook commands and help so Cobra can handle them.
func TryHook(args []string) bool {
	repoPath, agent, hookEvent, ok, handled := parseHookArgs(args)
	if !handled {
		return false
	}
	if ok {
		runHook(repoPath, agent, hookEvent)
	}
	return true
}

func parseHookArgs(args []string) (repoPath, agent, hookEvent string, ok, handled bool) {
	var values []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--repo" && i+1 < len(args):
			i++
			repoPath = args[i]
		case strings.HasPrefix(args[i], "--repo="):
			repoPath = strings.TrimPrefix(args[i], "--repo=")
		case args[i] == "--json":
		case args[i] == "-h" || args[i] == "--help":
			return "", "", "", false, false
		case args[i] == "hook" && !handled && len(values) == 0:
			handled = true
		case !handled:
			return "", "", "", false, false
		default:
			values = append(values, args[i])
		}
	}
	if len(values) == 2 {
		return repoPath, values[0], values[1], true, handled
	}
	return repoPath, "", "", false, handled
}

func runHook(repoPath, agent, hookEvent string) {
	cursorGlobal := agent == "cursor-global"
	geminiGlobal := agent == "gemini-global"
	qwenGlobal := agent == "qwen-global"
	ampGlobal := agent == "amp-global"
	kiloGlobal := agent == "kilo-global"
	opencodeV1Global := agent == "opencode-v1-global"
	opencodeGlobal := agent == "opencode-global" || opencodeV1Global
	factoryGlobal := agent == "factory-global"
	kiroGlobal := agent == "kiro-global"
	copilotGlobal := agent == "copilot-global"
	if copilotGlobal {
		agent = "copilot"
	}
	windsurfGlobal := agent == "windsurf-global"
	if windsurfGlobal {
		agent = "windsurf"
	}
	if factoryGlobal {
		agent = "factory"
	}
	if kiroGlobal {
		agent = "kiro"
	}
	if opencodeGlobal {
		agent = "opencode"
	}
	if kiloGlobal {
		agent = "kilo"
	}
	piGlobal := agent == "pi-global" || agent == "omp-global"
	if piGlobal {
		agent = strings.TrimSuffix(agent, "-global")
	}
	if cursorGlobal {
		agent = "cursor"
	}
	if geminiGlobal {
		agent = "gemini"
	}
	if qwenGlobal {
		agent = "qwen"
	}
	if ampGlobal {
		agent = "amp"
	}
	if agent == "gemini" || agent == "agy" || agent == "cursor" || agent == "qwen" {
		defer func() {
			if agent == "agy" && hookEvent == "Stop" {
				fmt.Fprintln(os.Stdout, `{"decision":"allow"}`)
			} else if agent == "cursor" && hookEvent == "beforeSubmitPrompt" {
				fmt.Fprintln(os.Stdout, `{"continue":true}`)
			} else if agent == "cursor" && hookEvent == "beforeReadFile" {
				fmt.Fprintln(os.Stdout, `{"permission":"allow"}`)
			} else {
				fmt.Fprintln(os.Stdout, "{}")
			}
		}()
	}
	if agent == "codex" && hookEvent == "Stop" {
		defer fmt.Fprintln(os.Stdout, "{}")
	}
	var b []byte
	if agent == "openhands" && repoPath == "" {
		var err error
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		repoPath, err = openhandsHookRoot(b)
		if err != nil {
			return
		}
	}
	if agent == "kiro" && repoPath == "" {
		var err error
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		repoPath, err = kiroHookRoot(b)
		if err != nil || (kiroGlobal && kiro.HasProjectHook(repoPath, hookEvent)) {
			return
		}
	}
	if agent == "goose" && repoPath == "" && os.Getenv("PLUGIN_ROOT") != "" {
		var err error
		repoPath, err = gooseProjectRoot(os.Getenv("PLUGIN_ROOT"))
		if err != nil {
			return
		}
	}
	if windsurfGlobal && repoPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return
		}
		repoPath, err = repo.FindWorktreeRoot(cwd)
		if err != nil || windsurf.HasProjectHook(repoPath, hookEvent) || (cwd != repoPath && windsurf.HasProjectHook(cwd, hookEvent)) {
			return
		}
	}
	if agent == "factory" && repoPath == "" {
		var err error
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		var payload struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(b, &payload) != nil {
			return
		}
		directory := os.Getenv("FACTORY_PROJECT_DIR")
		if directory == "" {
			directory = payload.CWD
		}
		if !filepath.IsAbs(directory) {
			return
		}
		repoPath, err = repo.FindWorktreeRoot(directory)
		if err != nil || (factoryGlobal && (factory.HasProjectHook(repoPath, hookEvent) || (directory != repoPath && factory.HasProjectHook(directory, hookEvent)))) {
			return
		}
	}
	if kiloGlobal && repoPath == "" {
		var err error
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		var payload struct {
			CWD           string `json:"cwd"`
			ProjectLoaded bool   `json:"project_loaded"`
		}
		if json.Unmarshal(b, &payload) != nil || !filepath.IsAbs(payload.CWD) {
			return
		}
		repoPath, err = repo.FindWorktreeRoot(payload.CWD)
		if err != nil || payload.ProjectLoaded {
			return
		}
	}
	if piGlobal && repoPath == "" {
		var err error
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		var payload struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(b, &payload) != nil || !filepath.IsAbs(payload.CWD) {
			return
		}
		repoPath, err = repo.FindWorktreeRoot(payload.CWD)
		if err != nil {
			return
		}
	}
	if agent == "qwen" && repoPath == "" {
		var err error
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		var payload struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(b, &payload) != nil || !filepath.IsAbs(payload.CWD) {
			return
		}
		repoPath, err = repo.FindWorktreeRoot(payload.CWD)
		if err != nil || (qwenGlobal && (qwen.HasProjectHook(repoPath, hookEvent) || (payload.CWD != repoPath && qwen.HasProjectHook(payload.CWD, hookEvent)))) {
			return
		}
	}
	if agent == "gemini" && repoPath == "" {
		var err error
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		var payload struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(b, &payload) != nil || !filepath.IsAbs(payload.CWD) {
			return
		}
		repoPath, err = repo.FindWorktreeRoot(payload.CWD)
		if err != nil || (geminiGlobal && (gemini.HasProjectHook(repoPath, hookEvent) || (payload.CWD != repoPath && gemini.HasProjectHook(payload.CWD, hookEvent)))) {
			return
		}
	}
	if agent == "cursor" && repoPath == "" {
		var err error
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		var payload struct {
			WorkspaceRoots []string `json:"workspace_roots"`
		}
		// Multiple workspace folders do not identify which repository owns a
		// prompt; do not attach the turn to an arbitrary project.
		if json.Unmarshal(b, &payload) != nil || len(payload.WorkspaceRoots) != 1 || !filepath.IsAbs(payload.WorkspaceRoots[0]) {
			return
		}
		repoPath, err = repo.FindWorktreeRoot(payload.WorkspaceRoots[0])
		if err != nil || (cursorGlobal && cursor.HasProjectHook(payload.WorkspaceRoots[0], hookEvent)) {
			return
		}
	}
	if agent == "cline" && repoPath == "" {
		var err error
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		var payload struct {
			WorkspaceRoots []string `json:"workspaceRoots"`
		}
		if json.Unmarshal(b, &payload) != nil || len(payload.WorkspaceRoots) != 1 || !filepath.IsAbs(payload.WorkspaceRoots[0]) {
			return
		}
		repoPath, err = repo.FindWorktreeRoot(payload.WorkspaceRoots[0])
		if err != nil || cline.HasProjectHook(repoPath, hookEvent) {
			return
		}
	}
	if (agent == "poolside" || agent == "claude-code" || agent == "openclaude" || agent == "codex" || agent == "kimi" || agent == "zoo" || ampGlobal || opencodeGlobal || copilotGlobal) && repoPath == "" {
		var err error
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		var payload struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(b, &payload) != nil || !filepath.IsAbs(payload.CWD) {
			return
		}
		repoPath, err = repo.FindWorktreeRoot(payload.CWD)
		if err != nil || (copilotGlobal && copilot.HasProjectHook(repoPath, hookEvent)) {
			return
		}
	}
	if agent == "auggie" && repoPath == "" {
		var err error
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		repoPath, err = auggieHookRoot(b)
		if err != nil {
			return
		}
	}
	root, err := hookRootFromFlag(repoPath)
	if err != nil {
		return
	}
	if opencodeV1Global {
		if _, err := os.Stat(filepath.Join(root, ".trace", "opencode.enabled")); err == nil {
			return
		}
	}
	// Hooks are the only long-lived Trace entry point, so recover an exited or
	// crashed daemon here. StartAsync returns immediately and never blocks the
	// agent; the event falls back to the spool until the daemon is ready.
	if strings.EqualFold(hookEvent, "SessionStart") || hookEvent == string(event.SessionStart) || hookEvent == "UserPromptSubmit" || hookEvent == "turn_start" || (agent == "copilot" && hookEvent == "userPromptSubmitted") || (agent == "gemini" && hookEvent == "BeforeAgent") || (agent == "cursor" && hookEvent == "beforeSubmitPrompt") || (agent == "hermes" && hookEvent == "pre_llm_call") || (agent == "openhands" && hookEvent == "user_prompt_submit") || (agent == "auggie" && hookEvent == "PromptSubmit") || (agent == "cline" && (hookEvent == "TaskStart" || hookEvent == "TaskResume")) || (agent == "windsurf" && hookEvent == "pre_user_prompt") {
		daemon.StartAsync(root)
	}
	if b == nil {
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
	}
	if agent == "openhands" {
		events, err := openhands.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "poolside" {
		events, err := poolside.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "hermes" {
		events, err := hermes.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "auggie" {
		events, err := auggie.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "claude-code" {
		events, err := claudecode.ParseEvents(hookEvent, b, root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "trace hook: invalid Claude payload")
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "cline" {
		events, err := cline.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "openclaude" {
		events, err := openclaude.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "kiro" {
		events, err := kiro.Parse(hookEvent, b, root, os.Getenv("USER_PROMPT"))
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "kimi" {
		events, err := kimi.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "agy" {
		events, err := agy.Parse(hookEvent, b, root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "trace hook: invalid Antigravity payload")
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "cursor" {
		events, err := cursor.Parse(hookEvent, b, root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "trace hook: invalid Cursor payload")
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "gemini" {
		events, err := gemini.Parse(hookEvent, b, root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "trace hook: invalid Gemini payload")
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "qwen" {
		events, err := qwen.Parse(hookEvent, b, root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "trace hook: invalid Qwen payload")
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "codex" {
		events, err := codexhook.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "grok" {
		events, err := grok.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "goose" {
		events, err := goose.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "factory" {
		events, err := factory.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "windsurf" {
		events, err := windsurf.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	if agent == "copilot" {
		events, err := copilot.Parse(hookEvent, b, root)
		if err != nil {
			return
		}
		_ = daemon.SendBatch(filepath.Join(root, ".trace"), events)
		return
	}
	ev, err := parseGenericHook(agent, hookEvent, b, root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "trace hook: invalid payload")
		return
	}
	_ = daemon.Send(filepath.Join(root, ".trace"), ev)
}

// Kiro's hook payload can identify a different workspace than the process cwd.
// Older payloads that omit cwd retain the documented project-directory fallback.
func kiroHookRoot(body []byte) (string, error) {
	var payload struct {
		CWD string `json:"cwd"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parsing Kiro hook cwd: %w", err)
	}
	dir := payload.CWD
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("finding Kiro hook directory: %w", err)
		}
	}
	return repo.FindWorktreeRoot(dir)
}

func openhandsHookRoot(body []byte) (string, error) {
	var payload struct {
		WorkingDir string `json:"working_dir"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parsing OpenHands hook directory: %w", err)
	}
	dir := payload.WorkingDir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("finding OpenHands hook directory: %w", err)
		}
	}
	return repo.FindWorktreeRoot(dir)
}

// Auggie can run with --workspace-root while its hook process has a different
// working directory. Only route multiple workspaces when they share a worktree.
func auggieHookRoot(body []byte) (string, error) {
	var payload struct {
		CWD            string   `json:"cwd"`
		WorkspaceRoots []string `json:"workspace_roots"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parsing Auggie hook cwd: %w", err)
	}
	projects := payload.WorkspaceRoots
	if len(projects) == 0 {
		projects = []string{payload.CWD}
	}
	var root string
	for _, project := range projects {
		if !filepath.IsAbs(project) {
			return "", fmt.Errorf("Auggie workspace must be absolute")
		}
		workspaceRoot, err := repo.FindWorktreeRoot(project)
		if err != nil {
			return "", fmt.Errorf("finding Auggie workspace: %w", err)
		}
		if root != "" && root != workspaceRoot {
			return "", fmt.Errorf("Auggie workspaces span multiple worktrees")
		}
		root = workspaceRoot
	}
	return root, nil
}

func parseGenericHook(agent, hookEvent string, b []byte, root string) (event.Event, error) {
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
