//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// hookPayload is one recorded or representative agent hook callback.
type hookPayload struct {
	Agent  string            `json:"agent"`
	Event  string            `json:"event"`
	Stdin  string            `json:"stdin"`
	Env    map[string]string `json:"env,omitempty"`
	Source string            `json:"source,omitempty"`
}

// hookAliases are the names user-level (global) hook installations invoke.
var hookAliases = map[string][]string{
	"amp":      {"amp-global"},
	"copilot":  {"copilot-global"},
	"cursor":   {"cursor-global"},
	"factory":  {"factory-global"},
	"gemini":   {"gemini-global"},
	"kilo":     {"kilo-global"},
	"kiro":     {"kiro-global"},
	"omp":      {"omp-global"},
	"opencode": {"opencode-global", "opencode-v1-global"},
	"pi":       {"pi-global"},
	"qwen":     {"qwen-global"},
	"windsurf": {"windsurf-global"},
}

func loadHookPayloads(t *testing.T) []hookPayload {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden", "hook_payloads.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payloads []hookPayload
	if err := json.Unmarshal(b, &payloads); err != nil {
		t.Fatal(err)
	}
	return payloads
}

// hookRepo is a repository whose daemon start-up is observable but inert:
// the hook creates .trace/daemon.lock when it starts a daemon, and the
// daemon then refuses to run because Git tracks a file under .trace.
func (s *sandbox) hookRepo(name string, batch bool) string {
	s.t.Helper()
	root := s.repo(name)
	writeFile(s.t, filepath.Join(root, ".trace", "tracked"), "keeps the started daemon from running\n")
	s.git(root, "add", "-f", ".trace/tracked")
	s.git(root, "commit", "-q", "-m", "track")
	if batch {
		// A durable, batch-capable daemon's markers: frames go straight to
		// the spool and multi-event callbacks are one batch frame.
		for _, marker := range []string{"daemon.pid", "durable.ready", "batch.ready"} {
			if err := os.WriteFile(filepath.Join(root, ".trace", marker), []byte("999999"), 0600); err != nil {
				s.t.Fatal(err)
			}
		}
	}
	return root
}

// hook runs one hook invocation and records what it printed and wrote,
// then removes everything it created so cases stay independent.
func (s *sandbox) hook(roots map[string]string, inv invocation) result {
	s.t.Helper()
	started := time.Now()
	before := s.tree(roots, started)
	r := s.run(inv)
	// A hook that starts a daemon leaves its lock file. The daemon runs
	// detached; wait for it so everything it writes is recorded with this
	// case and removed before the next one.
	for _, root := range roots {
		if _, err := os.Stat(filepath.Join(root, ".trace", "daemon.lock")); err == nil {
			waitForDaemons(s.t, "", false)
			break
		}
	}
	after := s.tree(roots, started)
	r.Files = changes(before, after)
	var created []string
	for path := range after {
		if _, ok := before[path]; !ok {
			created = append(created, path)
		}
	}
	// Deepest first, so directories are empty when removed.
	sort.Sort(sort.Reverse(sort.StringSlice(created)))
	for _, path := range created {
		label, rel, _ := strings.Cut(strings.TrimSuffix(path, "/"), "/")
		if err := os.Remove(filepath.Join(roots[label], filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			s.t.Fatal(err)
		}
	}
	return r
}

func canonicalHookAgent(name string) string {
	if name == "opencode-v1-global" {
		return "opencode"
	}
	return strings.TrimSuffix(name, "-global")
}

func TestGoldenHooks(t *testing.T) {
	payloads := loadHookPayloads(t)
	s := newSandbox(t)
	batch := s.hookRepo("batch", true)
	legacy := s.hookRepo("legacy", false)
	outside := filepath.Join(s.base, "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	g := newGolden(t, "hooks")
	rootsFor := func(root string) map[string]string {
		return map[string]string{"root": root, "home": s.home, "trace_home": s.traceHome, "outside": outside}
	}
	expand := func(root, v string) string {
		return strings.ReplaceAll(s.expand(v), "{{ROOT}}", root)
	}
	envFor := func(root string, p hookPayload) map[string]string {
		env := map[string]string{}
		for k, v := range p.Env {
			env[k] = expand(root, v)
		}
		return env
	}
	seen := map[string]int{}
	first := map[string]hookPayload{}
	var order []string
	for _, p := range payloads {
		n := seen[p.Agent+" "+p.Event]
		seen[p.Agent+" "+p.Event]++
		id := fmt.Sprintf("%s/%s#%d", p.Agent, p.Event, n)
		if _, ok := first[p.Agent]; !ok {
			first[p.Agent] = p
			order = append(order, p.Agent)
		}
		names := append([]string{p.Agent}, hookAliases[p.Agent]...)
		for _, name := range names {
			g.add("batch/"+strings.Replace(id, p.Agent, name, 1), s.hook(rootsFor(batch), invocation{dir: batch, args: []string{"hook", name, p.Event}, stdin: expand(batch, p.Stdin), env: envFor(batch, p)}))
		}
		g.add("legacy/"+id, s.hook(rootsFor(legacy), invocation{dir: legacy, args: []string{"hook", p.Agent, p.Event}, stdin: expand(legacy, p.Stdin), env: envFor(legacy, p)}))
	}
	// Variations of the first payload of each agent and alias: an explicit
	// root, other working directories, paused recording and empty input.
	for _, agent := range order {
		p := first[agent]
		for _, name := range append([]string{agent}, hookAliases[agent]...) {
			key := name + "/" + p.Event
			stdin, env := expand(batch, p.Stdin), envFor(batch, p)
			g.add("repo-flag/"+key, s.hook(rootsFor(batch), invocation{dir: outside, args: []string{"--repo", batch, "hook", name, p.Event}, stdin: stdin, env: env}))
			g.add("cwd-sub/"+key, s.hook(rootsFor(batch), invocation{dir: filepath.Join(batch, "sub"), args: []string{"hook", name, p.Event}, stdin: stdin, env: env}))
			g.add("cwd-outside/"+key, s.hook(rootsFor(batch), invocation{dir: outside, args: []string{"hook", name, p.Event}, stdin: stdin, env: env}))
			g.add("empty-stdin/"+key, s.hook(rootsFor(batch), invocation{dir: batch, args: []string{"hook", name, p.Event}, env: env}))
			marker := filepath.Join(s.traceHome, "agents", canonicalHookAgent(name)+".disabled")
			writeFile(t, marker, "disabled\n")
			g.add("agent-disabled/"+key, s.hook(rootsFor(batch), invocation{dir: batch, args: []string{"hook", name, p.Event}, stdin: stdin, env: env}))
			if err := os.RemoveAll(filepath.Join(s.traceHome, "agents")); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(s.traceHome, "disabled"), "disabled\n")
			g.add("all-disabled/"+key, s.hook(rootsFor(batch), invocation{dir: batch, args: []string{"hook", name, p.Event}, stdin: stdin, env: env}))
			if err := os.Remove(filepath.Join(s.traceHome, "disabled")); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Root resolution details that payload tables do not cover.
	extra := []struct {
		name  string
		dir   string
		args  []string
		stdin string
		env   map[string]string
	}{
		{"goose-plugin-root", outside, []string{"hook", "goose", "UserPromptSubmit"}, `{"session_id":"g1","prompt":"hi"}`, map[string]string{"PLUGIN_ROOT": filepath.Join(batch, ".agents", "plugins", "trace")}},
		{"goose-plugin-root-wrong", batch, []string{"hook", "goose", "UserPromptSubmit"}, `{"session_id":"g1","prompt":"hi"}`, map[string]string{"PLUGIN_ROOT": filepath.Join(batch, "plugins")}},
		{"goose-plugin-root-relative", batch, []string{"hook", "goose", "UserPromptSubmit"}, `{"session_id":"g1","prompt":"hi"}`, map[string]string{"PLUGIN_ROOT": ".agents/plugins/trace"}},
		{"factory-project-dir", outside, []string{"hook", "factory", "UserPromptSubmit"}, `{"session_id":"f1","prompt":"hi","cwd":"/nonexistent"}`, map[string]string{"FACTORY_PROJECT_DIR": batch}},
		{"factory-relative-cwd", batch, []string{"hook", "factory", "UserPromptSubmit"}, `{"session_id":"f1","prompt":"hi","cwd":"relative"}`, nil},
		{"kilo-global-project-loaded", outside, []string{"hook", "kilo-global", "turn_start"}, fmt.Sprintf(`{"session_id":"k1","prompt":"hi","cwd":%q,"project_loaded":true}`, batch), nil},
		{"kilo-global-project-not-loaded", outside, []string{"hook", "kilo-global", "turn_start"}, fmt.Sprintf(`{"session_id":"k1","prompt":"hi","cwd":%q,"project_loaded":false}`, batch), nil},
		{"cursor-two-workspaces", batch, []string{"hook", "cursor", "beforeSubmitPrompt"}, fmt.Sprintf(`{"conversation_id":"c1","prompt":"hi","workspace_roots":[%q,%q]}`, batch, outside), nil},
		{"cursor-relative-workspace", batch, []string{"hook", "cursor", "beforeSubmitPrompt"}, `{"conversation_id":"c1","prompt":"hi","workspace_roots":["rel"]}`, nil},
		{"cline-two-workspaces", batch, []string{"hook", "cline", "UserPromptSubmit"}, fmt.Sprintf(`{"taskId":"t1","workspaceRoots":[%q,%q]}`, batch, outside), nil},
		{"auggie-two-worktrees", batch, []string{"hook", "auggie", "PromptSubmit"}, fmt.Sprintf(`{"session_id":"a1","workspace_roots":[%q,%q]}`, batch, outside), nil},
		{"auggie-same-worktree", outside, []string{"hook", "auggie", "PromptSubmit"}, fmt.Sprintf(`{"session_id":"a1","prompt":"hi","workspace_roots":[%q,%q]}`, batch, filepath.Join(batch, "sub")), nil},
		{"openhands-working-dir", outside, []string{"hook", "openhands", "UserPromptSubmit"}, fmt.Sprintf(`{"session_id":"o1","prompt":"hi","working_dir":%q}`, filepath.Join(batch, "sub")), nil},
		{"kiro-payload-cwd", outside, []string{"hook", "kiro", "userPromptSubmit"}, fmt.Sprintf(`{"session_id":"k1","cwd":%q}`, batch), map[string]string{"USER_PROMPT": "from env"}},
		{"kiro-no-cwd", batch, []string{"hook", "kiro", "userPromptSubmit"}, `{"session_id":"k1"}`, map[string]string{"USER_PROMPT": "from env"}},
		{"claude-relative-cwd", batch, []string{"hook", "claude-code", "UserPromptSubmit"}, `{"session_id":"c1","prompt":"hi","cwd":"rel"}`, nil},
		{"generic-unknown-agent", batch, []string{"hook", "foo", "turn_start"}, `{"session_id":"x1","prompt":"hi","paths":["a.go"],"tool_input":{"file_path":"b.go"}}`, nil},
		{"generic-tool-input-path", batch, []string{"hook", "aider", "tool_edit"}, `{"session_id":"x1","tool_name":"edit","tool_input":{"file_path":"a.go"}}`, nil},
		{"generic-secret-path", batch, []string{"hook", "aider", "tool_edit"}, `{"session_id":"x1","paths":[".env","a.go"]}`, nil},
		{"generic-at", batch, []string{"hook", "pi", "turn_end"}, `{"session_id":"x1","at":1700000000000}`, nil},
		{"git-commit-head", batch, []string{"hook", "git", "git_commit"}, `{"git_sha":"{{HEAD}}"}`, nil},
		{"git-rewrite-rebase", batch, []string{"hook", "git", "git_rewrite"}, "rebase\n" + strings.Repeat("a", 40) + " " + strings.Repeat("b", 40) + "\n" + strings.Repeat("c", 40) + " " + strings.Repeat("d", 40) + "\nnot a pair\n", nil},
		{"git-other-event", batch, []string{"hook", "git", "turn_start"}, `{"session_id":"x"}`, nil},
		{"args-one", batch, []string{"hook", "cursor"}, `{}`, nil},
		{"args-three", batch, []string{"hook", "cursor", "stop", "extra"}, `{}`, nil},
		{"args-json-flag", batch, []string{"--json", "hook", "cursor", "stop"}, fmt.Sprintf(`{"conversation_id":"c1","workspace_roots":[%q]}`, batch), nil},
		{"args-repo-equals", outside, []string{"--repo=" + batch, "hook", "gemini", "BeforeAgent"}, `{"session_id":"g1","prompt":"hi"}`, nil},
		{"args-repo-after-hook", outside, []string{"hook", "--repo", batch, "gemini", "BeforeAgent"}, `{"session_id":"g1","prompt":"hi"}`, nil},
	}
	head := gitOutput(t, s, batch, "rev-parse", "HEAD")
	for _, tc := range extra {
		stdin := strings.ReplaceAll(tc.stdin, "{{HEAD}}", head)
		g.add("extra/"+tc.name, s.hook(rootsFor(batch), invocation{dir: tc.dir, args: tc.args, stdin: stdin, env: tc.env}))
	}
	// Agent names and events are matched exactly, except the common
	// session-start event, which also starts a stopped daemon in any case.
	cwd := func(root string) string { return fmt.Sprintf(`{"session_id":"v1","prompt":"hi","cwd":%q}`, root) }
	variants := []struct {
		name, root string
		args       []string
		stdin      string
	}{
		{"agent-Claude-Code", batch, []string{"hook", "Claude-Code", "Stop"}, cwd(batch)},
		{"agent-CURSOR", batch, []string{"hook", "CURSOR", "beforeSubmitPrompt"}, fmt.Sprintf(`{"conversation_id":"c1","prompt":"hi","workspace_roots":[%q]}`, batch)},
		{"alias-Cursor-Global", batch, []string{"hook", "Cursor-Global", "beforeSubmitPrompt"}, fmt.Sprintf(`{"conversation_id":"c1","prompt":"hi","workspace_roots":[%q]}`, batch)},
		{"alias-GEMINI-GLOBAL", batch, []string{"hook", "GEMINI-GLOBAL", "BeforeAgent"}, cwd(batch)},
		{"alias-codex-global", batch, []string{"hook", "codex-global", "Stop"}, cwd(batch)},
		{"event-codex-stop", batch, []string{"hook", "codex", "stop"}, cwd(batch)},
		{"event-codex-STOP", batch, []string{"hook", "codex", "STOP"}, cwd(batch)},
		{"event-agy-stop", batch, []string{"hook", "agy", "stop"}, cwd(batch)},
		{"event-cursor-BeforeSubmitPrompt", batch, []string{"hook", "cursor", "BeforeSubmitPrompt"}, fmt.Sprintf(`{"conversation_id":"c1","prompt":"hi","workspace_roots":[%q]}`, batch)},
		{"event-claude-userpromptsubmit", batch, []string{"hook", "claude-code", "userpromptsubmit"}, cwd(batch)},
		{"legacy-claude-sessionstart", legacy, []string{"hook", "claude-code", "sessionstart"}, cwd(legacy)},
		{"legacy-claude-SESSIONSTART", legacy, []string{"hook", "claude-code", "SESSIONSTART"}, cwd(legacy)},
		{"legacy-pi-session_start", legacy, []string{"hook", "pi", "session_start"}, cwd(legacy)},
		{"legacy-claude-userpromptsubmit", legacy, []string{"hook", "claude-code", "userpromptsubmit"}, cwd(legacy)},
		{"legacy-gemini-beforeagent", legacy, []string{"hook", "gemini", "beforeagent"}, cwd(legacy)},
		{"legacy-gemini-BeforeAgent", legacy, []string{"hook", "gemini", "BeforeAgent"}, cwd(legacy)},
		{"legacy-Gemini-BeforeAgent", legacy, []string{"hook", "Gemini", "BeforeAgent"}, cwd(legacy)},
	}
	for _, tc := range variants {
		g.add("case/"+tc.name, s.hook(rootsFor(tc.root), invocation{dir: tc.root, args: tc.args, stdin: tc.stdin}))
	}
	// Hooks read at most 4 MiB of payload: exactly 4 MiB of JSON decodes;
	// one byte more is truncated and invalid.
	prefix := fmt.Sprintf(`{"session_id":"big","prompt":"hi","cwd":%q,"pad":"`, batch)
	atLimit := prefix + strings.Repeat("x", 4<<20-len(prefix)-2) + `"}`
	overLimit := prefix + strings.Repeat("x", 4<<20-len(prefix)-1) + `"}`
	for _, name := range []string{"claude-code", "codex", "gemini-global", "aider"} {
		ev := map[string]string{"claude-code": "UserPromptSubmit", "codex": "UserPromptSubmit", "gemini-global": "BeforeAgent", "aider": "turn_start"}[name]
		g.add("payload-limit/"+name+"/at", s.hook(rootsFor(batch), invocation{dir: batch, args: []string{"hook", name, ev}, stdin: atLimit}))
		g.add("payload-limit/"+name+"/over", s.hook(rootsFor(batch), invocation{dir: batch, args: []string{"hook", name, ev}, stdin: overLimit}))
	}
	g.check()
}

func gitOutput(t *testing.T, s *sandbox, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = s.env(nil)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestGoldenGlobalHooksBesideProjectHooks covers user-level hooks in a
// repository whose own configuration already records that agent.
func TestGoldenGlobalHooksBesideProjectHooks(t *testing.T) {
	payloads := loadHookPayloads(t)
	s := newSandbox(t)
	g := newGolden(t, "hooks_with_project_config")
	for _, base := range []string{"amp", "cline", "copilot", "cursor", "factory", "gemini", "kilo", "kiro", "omp", "opencode", "pi", "qwen", "windsurf"} {
		root := s.repo("project-" + base)
		holdFakeDaemon(t, root)
		initRes := s.run(invocation{dir: root, args: []string{"init", "--agent", base}})
		if initRes.Exit != 0 {
			t.Fatalf("init --agent %s: %+v", base, initRes)
		}
		roots := map[string]string{"root": root, "home": s.home, "trace_home": s.traceHome}
		names := hookAliases[base]
		if base == "cline" {
			names = []string{"cline"}
		}
		seen := map[string]int{}
		for _, p := range payloads {
			if p.Agent != base {
				continue
			}
			n := seen[p.Event]
			seen[p.Event]++
			env := map[string]string{}
			for k, v := range p.Env {
				env[k] = strings.ReplaceAll(s.expand(v), "{{ROOT}}", root)
			}
			stdin := strings.ReplaceAll(s.expand(p.Stdin), "{{ROOT}}", root)
			for _, name := range names {
				key := fmt.Sprintf("%s/%s#%d", name, p.Event, n)
				g.add(key, s.hook(roots, invocation{dir: root, args: []string{"hook", name, p.Event}, stdin: stdin, env: env}))
				if n == 0 && len(seen) == 1 {
					g.add("repo-flag/"+key, s.hook(roots, invocation{dir: root, args: []string{"--repo", root, "hook", name, p.Event}, stdin: stdin, env: env}))
				}
			}
		}
	}
	g.check()
}

// TestGoldenProjectHooksInSubdirectory covers agents whose project
// configuration sits in a subdirectory of the repository, invoked from that
// subdirectory and from the root, by their project hooks and their
// user-level aliases.
func TestGoldenProjectHooksInSubdirectory(t *testing.T) {
	payloads := loadHookPayloads(t)
	s := newSandbox(t)
	g := newGolden(t, "hooks_subdir")
	for _, base := range []string{"amp", "cline", "copilot", "cursor", "factory", "gemini", "kilo", "kiro", "omp", "opencode", "pi", "qwen", "windsurf"} {
		root := s.repo("subdir-" + base)
		holdFakeDaemon(t, root)
		if r := s.run(invocation{dir: root, args: []string{"init", "--agent", base}}); r.Exit != 0 {
			t.Fatalf("init --agent %s: %+v", base, r)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		var moved []string
		for _, entry := range entries {
			if name := entry.Name(); strings.HasPrefix(name, ".") && name != ".git" && name != ".trace" {
				if err := os.Rename(filepath.Join(root, name), filepath.Join(root, "sub", name)); err != nil {
					t.Fatal(err)
				}
				moved = append(moved, name)
			}
		}
		if len(moved) == 0 {
			t.Fatalf("%s: no project configuration to move", base)
		}
		sub := filepath.Join(root, "sub")
		roots := map[string]string{"root": root, "home": s.home, "trace_home": s.traceHome}
		var first *hookPayload
		for i := range payloads {
			if payloads[i].Agent == base {
				first = &payloads[i]
				break
			}
		}
		for _, name := range append([]string{base}, hookAliases[base]...) {
			events := []string{first.Event}
			for _, ev := range []string{"SessionStart", "Stop"} {
				if ev != first.Event {
					events = append(events, ev)
				}
			}
			for _, ev := range events {
				stdin := first.Stdin
				if ev != first.Event {
					stdin = `{"session_id":"sub-1","cwd":"{{ROOT}}"}`
				}
				for _, dir := range []string{"sub", "root"} {
					cwd, payloadRoot := sub, sub
					if dir == "root" {
						cwd = root
					}
					env := map[string]string{}
					for k, v := range first.Env {
						env[k] = strings.ReplaceAll(s.expand(v), "{{ROOT}}", payloadRoot)
					}
					in := strings.ReplaceAll(s.expand(stdin), "{{ROOT}}", payloadRoot)
					key := fmt.Sprintf("%s/%s/cwd-%s", name, ev, dir)
					g.add(key, s.hook(roots, invocation{dir: cwd, args: []string{"hook", name, ev}, stdin: in, env: env}))
				}
			}
		}
	}
	g.check()
}
