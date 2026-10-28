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
	"github.com/UseTrace-SI/trace-cli/internal/adapters/claudecode"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/cursor"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/gemini"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/event"
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
	if agent == "gemini" || agent == "agy" || agent == "cursor" {
		defer func() {
			if agent == "agy" && hookEvent == "Stop" {
				fmt.Fprintln(os.Stdout, `{"decision":"allow"}`)
			} else if agent == "cursor" && hookEvent == "beforeSubmitPrompt" {
				fmt.Fprintln(os.Stdout, `{"continue":true}`)
			} else {
				fmt.Fprintln(os.Stdout, "{}")
			}
		}()
	}
	root, err := hookRootFromFlag(repoPath)
	if err != nil {
		return
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	if err != nil {
		return
	}
	if agent == "claude-code" {
		events, err := claudecode.Parse(hookEvent, b, root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "trace hook: invalid Claude payload")
			return
		}
		for _, ev := range events {
			_ = daemon.Send(filepath.Join(root, ".trace"), ev)
		}
		return
	}
	if agent == "agy" {
		events, err := agy.Parse(hookEvent, b, root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "trace hook: invalid Antigravity payload")
			return
		}
		for _, ev := range events {
			_ = daemon.Send(filepath.Join(root, ".trace"), ev)
		}
		return
	}
	if agent == "cursor" {
		events, err := cursor.Parse(hookEvent, b, root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "trace hook: invalid Cursor payload")
			return
		}
		for _, ev := range events {
			_ = daemon.Send(filepath.Join(root, ".trace"), ev)
		}
		return
	}
	if agent == "gemini" {
		events, err := gemini.Parse(hookEvent, b, root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "trace hook: invalid Gemini payload")
			return
		}
		for _, ev := range events {
			_ = daemon.Send(filepath.Join(root, ".trace"), ev)
		}
		return
	}
	var p map[string]json.RawMessage
	if len(b) > 0 && json.Unmarshal(b, &p) != nil {
		fmt.Fprintln(os.Stderr, "trace hook: invalid payload")
		return
	}
	ev := event.Event{Kind: event.Kind(hookEvent), Agent: agent, Worktree: root, At: time.Now().UnixMilli()}
	str := func(key string) string { var v string; _ = json.Unmarshal(p[key], &v); return v }
	ev.AgentSessionID = str("session_id")
	ev.Prompt = str("prompt")
	ev.ToolName = str("tool_name")
	ev.TranscriptPath = str("transcript_path")
	ev.GitSHA = str("git_sha")
	var input map[string]json.RawMessage
	_ = json.Unmarshal(p["tool_input"], &input)
	var filePath string
	_ = json.Unmarshal(input["file_path"], &filePath)
	if filePath != "" {
		ev.Paths = []string{filePath}
	}
	_ = daemon.Send(filepath.Join(root, ".trace"), ev)
}
