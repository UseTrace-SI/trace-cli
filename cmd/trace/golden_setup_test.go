//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// projectAgents are the agents `trace init --agent` and `trace agent add`
// accept.
var projectAgents = []string{"agy", "aider", "amp", "claude-code", "cline", "codebuff", "codex", "copilot", "crewai", "cursor", "factory", "gemini", "goose", "grok", "kilo", "kiro", "langgraph", "omp", "openclaude", "opencode", "openhands", "pi", "qwen", "windsurf"}

// globalAgents are the agents `trace setup --agent` accepts.
var globalAgents = []string{"amp", "auggie", "claude-code", "cline", "codex", "copilot", "cursor", "factory", "gemini", "hermes", "kilo", "kimi", "kiro", "omp", "openclaude", "opencode", "openhands", "pi", "poolside", "qwen", "windsurf", "zoo"}

const userHooks = `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo user-stop"}]}]}}`

// projectSeeds are user files that exist before Trace is installed; they
// must be merged, never overwritten.
var projectSeeds = map[string]map[string]string{
	"":            {".mcp.json": `{"mcpServers":{"user":{"command":"user-mcp"}}}`, ".git/hooks/post-commit": "#!/bin/sh\necho user-hook\n"},
	"claude-code": {".claude/settings.local.json": `{"permissions":{"allow":["Bash(ls)"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo user-stop"}]}]}}`},
	"openclaude":  {".openclaude/settings.local.json": `{"permissions":{"allow":["Bash(ls)"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo user-stop"}]}]}}`},
	"cursor":      {".cursor/hooks.json": `{"version":1,"hooks":{"stop":[{"command":"echo user-stop"}]}}`},
	"gemini":      {".gemini/settings.json": `{"theme":"Dark","hooks":{"AfterTool":[{"hooks":[{"type":"command","command":"echo user"}]}]}}`},
	"qwen":        {".qwen/settings.json": `{"theme":"Dark","hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"echo user"}]}]}}`},
	"factory":     {".factory/hooks.json": `{"Stop":[{"hooks":[{"type":"command","command":"echo user"}]}]}`},
	"openhands":   {".openhands/README": "user project hooks directory\n"},
}

var globalSeeds = map[string]map[string]string{
	"claude-code": {".claude/settings.json": userHooks},
	"codex":       {".codex/hooks.json": userHooks, ".codex/config.toml": "model = \"gpt\"\n"},
	"cursor":      {".cursor/hooks.json": `{"version":1,"hooks":{"stop":[{"command":"echo user-stop"}]}}`},
	"gemini":      {".gemini/settings.json": userHooks},
	"qwen":        {".qwen/settings.json": userHooks},
	"factory":     {".factory/hooks.json": `{"Stop":[{"hooks":[{"type":"command","command":"echo user"}]}]}`},
	"openhands":   {".openhands/hooks.json": `{"Stop":[{"hooks":[{"type":"command","command":"echo user"}]}]}`},
	"openclaude":  {".openclaude/settings.json": userHooks},
	"auggie":      {".augment/settings.json": userHooks},
	"kimi":        {".kimi-code/config.toml": "model = \"k2\"\n\n[[hooks]]\nevent = \"Stop\"\ncommand = \"echo user\"\n"},
	"poolside":    {".config/poolside/settings.yaml": "theme: dark\n"},
	"windsurf":    {".codeium/windsurf/hooks.json": `{"hooks":{"post_write_code":[{"command":"echo user"}]}}`},
}

func seed(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		writeFile(t, path, content)
		if strings.HasPrefix(rel, ".git/hooks/") {
			if err := os.Chmod(path, 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// steps runs commands in order, recording each one's output and the files
// it changed under roots. The first step records the complete tree.
func (s *sandbox) steps(g *golden, prefix string, roots map[string]string, dir string, env map[string]string, commands ...[]string) {
	s.t.Helper()
	before := s.tree(roots, time.Now())
	for i, args := range commands {
		started := time.Now()
		r := s.run(invocation{dir: dir, args: args, env: env})
		after := s.tree(roots, started)
		if i == 0 {
			r.Files = after
		} else {
			r.Files = changes(before, after)
		}
		before = after
		g.add(fmt.Sprintf("%s/%02d %s", prefix, i, strings.Join(args, " ")), r)
	}
}

func TestGoldenProjectInstall(t *testing.T) {
	g := newGolden(t, "project")
	for _, variant := range []string{"clean", "seeded"} {
		for _, agent := range projectAgents {
			s := newSandbox(t)
			root := s.repo("repo")
			if variant == "seeded" {
				seed(t, root, projectSeeds[""])
				seed(t, root, projectSeeds[agent])
			}
			holdFakeDaemon(t, root)
			roots := map[string]string{"root": root, "home": s.home, "trace_home": s.traceHome}
			s.steps(g, variant+"/"+agent, roots, root, nil,
				[]string{"init", "--agent", agent},
				[]string{"--json", "init", "--agent", agent},
				[]string{"agent", "remove", agent},
				[]string{"--json", "agent", "remove", agent},
				[]string{"agent", "add", agent},
				[]string{"--json", "agent", "add", agent},
				[]string{"uninstall"},
				[]string{"--json", "agent", "add", agent},
				[]string{"--json", "uninstall"},
			)
		}
	}
	// Defaults, unsupported names and an uninitialized repository.
	s := newSandbox(t)
	root := s.repo("repo")
	roots := map[string]string{"root": root, "home": s.home, "trace_home": s.traceHome}
	s.steps(g, "errors", roots, root, nil,
		[]string{"agent", "add", "cursor"},
		[]string{"agent", "remove", "cursor"},
		[]string{"init", "--agent", "nope"},
		[]string{"init", "--agent", "hermes"},
	)
	holdFakeDaemon(t, root)
	s.steps(g, "default", roots, root, nil,
		[]string{"init"},
		[]string{"agent", "add", "nope"},
		[]string{"agent", "remove", "nope"},
		[]string{"agent", "add", "zoo"},
		[]string{"agent", "add"},
		[]string{"--json", "agent", "add", "hermes"},
	)
	g.check()
}

func TestGoldenGlobalSetup(t *testing.T) {
	g := newGolden(t, "global")
	for _, variant := range []string{"clean", "seeded"} {
		for _, agent := range globalAgents {
			s := newSandbox(t)
			if variant == "seeded" {
				seed(t, s.home, globalSeeds[agent])
			}
			roots := map[string]string{"home": s.home, "trace_home": s.traceHome}
			s.steps(g, variant+"/"+agent, roots, s.home, nil,
				[]string{"setup", "--agent", agent},
				[]string{"--json", "setup", "--agent", agent},
				[]string{"setup", "--agent", agent, "--remove"},
				[]string{"--json", "setup", "--agent", agent, "--remove"},
				[]string{"--json", "enable", "--agent", agent},
				[]string{"disable", "--agent", agent},
				[]string{"--json", "disable", agent},
				[]string{"enable"},
				[]string{"--json", "enable"},
				[]string{"enable", agent},
				[]string{"disable"},
				[]string{"enable", "--agent", agent},
			)
		}
	}
	// Combinations, defaults, configuration directories and errors.
	combos := [][]string{
		{"setup"},
		{"--json", "setup"},
		{"setup", "--remove"},
		{"setup", "--agent", "codex,gemini,qwen,copilot,windsurf,opencode,kilo,pi,omp,amp,hermes,zoo,poolside,kiro"},
		{"--json", "setup", "--agent", "codex,gemini,qwen,copilot,windsurf,opencode,kilo,pi,omp,amp,hermes,zoo,poolside,kiro"},
		{"setup", "--agent", "codex,gemini,qwen,copilot,windsurf,opencode,kilo,pi,omp,amp,hermes,zoo,poolside,kiro", "--remove"},
		{"--json", "setup", "--agent", "zoo,claude-code", "--remove"},
		{"setup", "--agent", "zoo,claude-code", "--remove"},
		{"setup", "--agent", "cursor,factory,cline,auggie,openhands,kimi,openclaude"},
		{"setup", "--agent", "pi,omp"},
		{"setup", "--agent", "claude-code,nope"},
		{"setup", "--agent", "agy"},
		{"enable", "--agent", "nope"},
		{"enable", "--all", "--agent", "codex"},
		{"enable", "codex", "--agent", "codex"},
		{"enable", "--remove"},
		{"disable", "nope"},
		{"disable", "codex", "--agent", "codex"},
		{"--json", "disable"},
		{"--json", "enable", "codex", "gemini"},
	}
	s := newSandbox(t)
	roots := map[string]string{"home": s.home, "trace_home": s.traceHome}
	s.steps(g, "combo", roots, s.home, nil, combos...)
	for _, env := range []map[string]string{
		{"CLAUDE_CONFIG_DIR": "{{HOME}}/claude-config"},
		{"CLAUDE_CONFIG_DIR": "relative/claude"},
		{"OPENCLAUDE_CONFIG_DIR": "{{HOME}}/openclaude-config"},
		{"OPENCLAUDE_CONFIG_DIR": "relative-openclaude"},
	} {
		s := newSandbox(t)
		expanded := map[string]string{}
		var name []string
		for k, v := range env {
			expanded[k] = s.expand(v)
			name = append(name, k+"="+v)
		}
		agent := "claude-code"
		if _, ok := env["OPENCLAUDE_CONFIG_DIR"]; ok {
			agent = "openclaude"
		}
		roots := map[string]string{"home": s.home, "trace_home": s.traceHome}
		s.steps(g, "env "+strings.Join(name, " "), roots, s.home, expanded,
			[]string{"setup", "--agent", agent},
			[]string{"setup", "--agent", agent, "--remove"},
		)
	}
	g.check()
}

// TestGoldenDetection covers agent discovery from commands on PATH and
// profile directories in HOME.
func TestGoldenDetection(t *testing.T) {
	for _, dir := range []string{"/Applications", filepath.Join(os.Getenv("HOME"), "Applications")} {
		for _, app := range []string{"Codex.app", "Cursor.app", "Windsurf.app", "Devin.app", "OpenCode.app"} {
			if _, err := os.Stat(filepath.Join(dir, app)); err == nil {
				t.Skipf("%s is installed in %s; detection results would differ from the recording", app, dir)
			}
		}
	}
	g := newGolden(t, "detection")
	s := newSandbox(t)
	roots := map[string]string{"home": s.home, "trace_home": s.traceHome}
	s.steps(g, "none", roots, s.home, nil,
		[]string{"agents"},
		[]string{"--json", "agents"},
		[]string{"enable"},
		[]string{"--json", "enable", "--all"},
	)
	// A version probe and some installed commands and profiles.
	for name, script := range map[string]string{
		"pool":     "#!/bin/sh\necho 'pool 1.0.10'\n",
		"agent":    "#!/bin/sh\nexit 0\n",
		"droid":    "#!/bin/sh\nexit 0\n",
		"kiro-cli": "#!/bin/sh\nexit 0\n",
	} {
		path := filepath.Join(s.pathDir, name)
		writeFile(t, path, script)
		if err := os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{".gemini", ".config/amp", ".vscode/extensions/zoocodeorganization.zoo-code-1.2.3", ".hermes"} {
		if err := os.MkdirAll(filepath.Join(s.home, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	s.steps(g, "some", roots, s.home, nil,
		[]string{"agents"},
		[]string{"--json", "agents"},
		[]string{"--json", "enable", "--all"},
		[]string{"agents"},
		[]string{"disable", "gemini"},
		[]string{"--json", "agents"},
		[]string{"enable"},
		[]string{"disable"},
		[]string{"--json", "agents"},
		[]string{"enable", "--agent", "hermes"},
		[]string{"--json", "agents"},
		[]string{"setup", "--agent", "poolside"},
	)
	g.check()
}
