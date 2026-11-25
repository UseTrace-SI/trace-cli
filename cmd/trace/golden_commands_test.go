//go:build !windows

package main

import (
	"path/filepath"
	"testing"
)

// launchAgents are the agents fork and resume can launch, reviewAgents the
// headless reviewers and runAgents the agents `trace run` accepts.
var (
	launchAgents = []string{"agy", "claude-code", "codex", "cursor"}
	reviewAgents = []string{"claude-code", "codex"}
	runAgents    = []string{"aider", "continue", "crush", "vibe"}
)

// agentPrograms are the programs those agents run.
var agentPrograms = []string{"agent", "agy", "aider", "claude", "cn", "codex", "crush", "vibe"}

// TestGoldenCommands covers the commands that start agents and the help
// text generated from the agent registry.
func TestGoldenCommands(t *testing.T) {
	g := newGolden(t, "commands")

	s := newSandbox(t)
	roots := map[string]string{"home": s.home, "trace_home": s.traceHome}
	s.changedSteps(g, "help", roots, s.home, nil,
		[]string{"--help"},
		[]string{"setup", "--help"},
		[]string{"enable", "--help"},
		[]string{"disable", "--help"},
		[]string{"agents", "--help"},
		[]string{"init", "--help"},
		[]string{"agent", "--help"},
		[]string{"agent", "add", "--help"},
		[]string{"agent", "remove", "--help"},
		[]string{"uninstall", "--help"},
		[]string{"run", "--help"},
		[]string{"review", "--help"},
		[]string{"fork", "--help"},
		[]string{"resume", "--help"},
		[]string{"hook", "--help"},
	)

	// trace run without the agent's program, then with stubs that log
	// their arguments.
	s = newSandbox(t)
	root := s.repo("repo")
	out := filepath.Join(s.base, "out")
	writeFile(t, filepath.Join(out, ".keep"), "")
	env := map[string]string{"TRACE_STUB_DIR": out}
	roots = map[string]string{"root": root, "home": s.home, "trace_home": s.traceHome, "out": out}
	missing := [][]string{{"run", "nope", "--prompt", "hi"}, {"run", "Vibe", "--prompt", "hi"}, {"run", "codex", "--prompt", "hi"}, {"run", "vibe"}, {"run"}}
	for _, agent := range runAgents {
		missing = append(missing, []string{"run", agent, "--prompt", "hi"})
	}
	s.changedSteps(g, "run-missing", roots, root, env, missing...)
	for _, program := range agentPrograms {
		s.stubAgent(program)
	}
	var runs [][]string
	for _, agent := range runAgents {
		runs = append(runs, []string{"run", agent, "--prompt", "change a.go"})
	}
	s.changedSteps(g, "run", roots, root, env, runs...)

	// Headless reviews of an unstaged change.
	s = newSandbox(t)
	root = s.repo("repo")
	holdFakeDaemon(t, root)
	out = filepath.Join(s.base, "out")
	writeFile(t, filepath.Join(out, ".keep"), "")
	env = map[string]string{"TRACE_STUB_DIR": out}
	roots = map[string]string{"root": root, "home": s.home, "trace_home": s.traceHome, "out": out}
	if r := s.run(invocation{dir: root, args: []string{"init", "--agent", "claude-code"}}); r.Exit != 0 {
		t.Fatalf("init: %+v", r)
	}
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc A() {}\n")
	review := [][]string{{"review", "--agent", "codex"}}
	for _, agent := range append(append([]string{}, reviewAgents...), "cursor", "nope", "Codex", "") {
		review = append(review, []string{"review", "--no-launch", "--agent", agent}, []string{"--json", "review", "--no-launch", "--agent", agent})
	}
	review = append(review,
		[]string{"review", "--no-launch", "--agents", "claude-code,codex"},
		[]string{"--json", "review", "--no-launch", "--agents", "codex,claude-code"},
		[]string{"review", "--no-launch", "--agents", "codex,codex"},
		[]string{"review", "--no-launch", "--agents", "claude-code,cursor"},
		[]string{"review", "--no-launch", "--agent", "codex", "--agents", "codex"},
		[]string{"review", "--no-launch"},
		[]string{"review", "--no-launch", "--staged"},
	)
	s.changedSteps(g, "review-missing", roots, root, env, review...)
	for _, program := range agentPrograms {
		s.stubAgent(program)
	}
	s.changedSteps(g, "review", roots, root, env,
		[]string{"review", "--agent", "claude-code"},
		[]string{"review", "--agent", "codex"},
		[]string{"--json", "review", "--agent", "codex"},
		[]string{"review", "--agents", "codex"},
		[]string{"review", "--agent", "cursor"},
		[]string{"review", "--agent", "agy"},
	)

	// Interoperability: opt-in trailers, exports and importers.
	s = newSandbox(t)
	root = s.repo("repo")
	holdFakeDaemon(t, root)
	out = filepath.Join(s.base, "out")
	writeFile(t, filepath.Join(out, ".keep"), "")
	roots = map[string]string{"root": root, "home": s.home, "trace_home": s.traceHome, "out": out}
	if r := s.run(invocation{dir: root, args: []string{"init", "--agent", "claude-code"}}); r.Exit != 0 {
		t.Fatalf("init: %+v", r)
	}
	s.changedSteps(g, "interop", roots, root, nil,
		[]string{"config", "get", "commit-trailer"},
		[]string{"config", "set", "commit-trailer", "on"},
		[]string{"--json", "config", "get", "commit-trailer"},
		[]string{"config", "set", "commit-trailer", "off"},
		[]string{"config", "set", "nope", "on"},
		[]string{"--json", "export", "--format", "agent-trace", "--rev", "HEAD", "--out", "../out/trace.jsonl"},
		[]string{"export", "--format", "markdown"},
		[]string{"--json", "export", "--format", "markdown", "--out", "../out/md"},
		[]string{"export", "--format", "markdown", "--out", "../out/md2"},
		[]string{"export", "--format", "nope"},
		[]string{"import", "--from", "entire"},
		[]string{"--json", "import", "--from", "git-ai"},
	)

	// Resume rewinds and launches the agent with the context; fork also
	// creates a worktree, whose new daemon is stopped after each case.
	s = newSandbox(t)
	root = s.repo("repo")
	holdFakeDaemon(t, root)
	out = filepath.Join(s.base, "out")
	writeFile(t, filepath.Join(out, ".keep"), "")
	env = map[string]string{"TRACE_STUB_DIR": out}
	roots = map[string]string{"root": root, "home": s.home, "trace_home": s.traceHome, "out": out}
	if r := s.run(invocation{dir: root, args: []string{"init", "--agent", "claude-code"}}); r.Exit != 0 {
		t.Fatalf("init: %+v", r)
	}
	for _, program := range agentPrograms {
		s.stubAgent(program)
	}
	resume := [][]string{{"resume", "0"}}
	for _, agent := range append(append([]string{}, launchAgents...), "gemini", "nope", "Claude-Code", "cursor-global", "") {
		resume = append(resume, []string{"resume", "0", "--agent", agent})
	}
	resume = append(resume, []string{"--json", "resume", "0", "--agent", "codex"}, []string{"resume", "0", "--agent", "codex", "--no-launch"}, []string{"resume", "0", "--agent", "nope", "--no-launch"})
	s.changedSteps(g, "resume", roots, root, env, resume...)
	for i, agent := range append(append([]string{}, launchAgents...), "nope") {
		name := "f" + string(rune('a'+i))
		fork := filepath.Join(s.base, "repo-fork-0-"+name)
		forkRoots := map[string]string{"root": root, "out": out}
		s.changedSteps(g, "fork/"+agent, forkRoots, root, env, []string{"fork", "0", "--agent", agent, "--name", name})
		waitForDaemons(t, fork, true)
	}
	fork := filepath.Join(s.base, "repo-fork-0-nolaunch")
	s.changedSteps(g, "fork/no-launch", map[string]string{"root": root, "out": out}, root, env, []string{"--json", "fork", "0", "--agent", "codex", "--name", "nolaunch", "--no-launch"})
	waitForDaemons(t, fork, true)
	waitForDaemons(t, "", false)
	g.check()
}
