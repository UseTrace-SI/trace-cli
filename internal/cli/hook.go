package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/agy"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/claudecode"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/cursor"
	"github.com/UseTrace-SI/trace-cli/internal/adapters/gemini"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func hookCommand(repoPath *string) *cobra.Command {
	return &cobra.Command{Use: "hook <agent> <event>", Hidden: true, Run: func(_ *cobra.Command, args []string) {
		if len(args) != 2 {
			return
		}
		if args[0] == "gemini" || args[0] == "agy" || args[0] == "cursor" {
			defer func() {
				if args[0] == "agy" && args[1] == "Stop" {
					fmt.Fprintln(os.Stdout, `{"decision":"allow"}`)
				} else if args[0] == "cursor" && args[1] == "beforeSubmitPrompt" {
					fmt.Fprintln(os.Stdout, `{"continue":true}`)
				} else {
					fmt.Fprintln(os.Stdout, "{}")
				}
			}()
		}
		root, err := rootFromFlag(*repoPath)
		if err != nil {
			return
		}
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
		if err != nil {
			return
		}
		if args[0] == "claude-code" {
			events, err := claudecode.Parse(args[1], b, root)
			if err != nil {
				fmt.Fprintln(os.Stderr, "trace hook: invalid Claude payload")
				return
			}
			for _, ev := range events {
				_ = daemon.Send(filepath.Join(root, ".trace"), ev)
			}
			return
		}
		if args[0] == "agy" {
			events, err := agy.Parse(args[1], b, root)
			if err != nil {
				fmt.Fprintln(os.Stderr, "trace hook: invalid Antigravity payload")
				return
			}
			for _, ev := range events {
				_ = daemon.Send(filepath.Join(root, ".trace"), ev)
			}
			return
		}
		if args[0] == "cursor" {
			events, err := cursor.Parse(args[1], b, root)
			if err != nil {
				fmt.Fprintln(os.Stderr, "trace hook: invalid Cursor payload")
				return
			}
			for _, ev := range events {
				_ = daemon.Send(filepath.Join(root, ".trace"), ev)
			}
			return
		}
		if args[0] == "gemini" {
			events, err := gemini.Parse(args[1], b, root)
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
		ev := event.Event{Kind: event.Kind(args[1]), Agent: args[0], Worktree: root, At: time.Now().UnixMilli()}
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
	}}
}
