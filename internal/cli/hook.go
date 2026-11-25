package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/adapters"
	"github.com/UseTrace-SI/trace-cli/internal/daemon"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/userstate"
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

func runHook(repoPath, name, hookEvent string) {
	// Registered first so it runs last: after an unexpected panic the deferred
	// agent responses below still print and the hook exits 0, not with the
	// status 2 that agents treat as a blocking hook failure.
	defer func() {
		if recover() != nil {
			fmt.Fprintln(os.Stderr, "trace hook: internal error")
		}
	}()
	agent, global := adapters.ForHook(name)
	if response, ok := agent.HookResponse(hookEvent); ok {
		defer fmt.Fprintln(os.Stdout, response)
	}
	if userstate.DisabledFor(agent.Name()) {
		return
	}
	call := adapters.NewHookCall(agent.Name(), name, hookEvent, global, os.Stdin)
	if repoPath == "" {
		var err error
		if repoPath, err = agent.ResolveHookRoot(call); err != nil {
			return
		}
	}
	root, err := hookRootFromFlag(repoPath)
	if err != nil {
		return
	}
	if agent.SkipHook(call, root) {
		return
	}
	// Hooks are the only long-lived Trace entry point, so recover an exited or
	// crashed daemon here. StartAsync returns immediately and never blocks the
	// agent; the event falls back to the spool until the daemon is ready.
	if startsSession(hookEvent) || agent.StartsDaemon(hookEvent) {
		daemon.StartAsync(root)
	}
	body, err := call.Body()
	if err != nil {
		return
	}
	events, err := agent.DecodeHook(call, body, root)
	if err != nil {
		if message := agent.InvalidPayloadMessage(); message != "" {
			fmt.Fprintln(os.Stderr, message)
		}
		return
	}
	dir := filepath.Join(root, ".trace")
	if agent.FramePerEvent() {
		for _, ev := range events {
			_ = daemon.Send(dir, ev)
		}
		return
	}
	_ = daemon.SendBatch(dir, events)
}

// startsSession reports the session and turn start events every agent
// shares.
func startsSession(hookEvent string) bool {
	return strings.EqualFold(hookEvent, "SessionStart") || hookEvent == string(event.SessionStart) || hookEvent == "UserPromptSubmit" || hookEvent == "turn_start"
}
