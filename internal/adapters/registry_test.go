package adapters

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRegistryNamesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, a := range All() {
		names := append([]string{a.Name()}, a.HookAliases()...)
		for _, name := range names {
			if other, ok := seen[name]; ok {
				t.Errorf("%q is claimed by %s and %s", name, other, a.Name())
			}
			seen[name] = a.Name()
		}
		if a.Capabilities().DisplayName == "" {
			t.Errorf("%s has no display name", a.Name())
		}
		if v := a.Capabilities().Verification; v != Verified && v != Preview {
			t.Errorf("%s has verification %q", a.Name(), v)
		}
	}
}

func TestForHook(t *testing.T) {
	for _, tc := range []struct {
		name, agent string
		global      bool
	}{
		{"claude-code", "claude-code", false},
		{"cursor-global", "cursor", true},
		{"opencode-v1-global", "opencode", true},
		{"opencode-global", "opencode", true},
		{"omp-global", "omp", true},
		{"pi", "pi", false},
		{"git", "git", false},
		{"unknown-agent", "unknown-agent", false},
		{"claude-code-global", "claude-code-global", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, global := ForHook(tc.name)
			if a.Name() != tc.agent || global != tc.global {
				t.Fatalf("ForHook(%q) = %s, %t; want %s, %t", tc.name, a.Name(), global, tc.agent, tc.global)
			}
		})
	}
}

func TestCapabilityLookups(t *testing.T) {
	for _, tc := range []struct {
		name            string
		project, global bool
	}{
		{"claude-code", true, true},
		{"codex", true, true},
		{"opencode", true, true},
		{"agy", true, false},
		{"aider", true, false},
		{"kimi", false, true},
		{"zoo", false, true},
		{"git", false, false},
		{"nope", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := ForProject(tc.name); ok != tc.project {
				t.Errorf("ForProject = %t, want %t", ok, tc.project)
			}
			if _, ok := ForGlobal(tc.name); ok != tc.global {
				t.Errorf("ForGlobal = %t, want %t", ok, tc.global)
			}
		})
	}
}

// Every project hook installer is removed by trace uninstall exactly once.
func TestProjectRemoversCoverInstallers(t *testing.T) {
	var want, got []string
	for _, a := range All() {
		if _, ok := a.(ProjectInstaller); ok {
			want = append(want, a.Name())
		}
	}
	for _, p := range ProjectRemovers() {
		got = append(got, p.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("removers %v, installers %v", got, want)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("removers %v, installers %v", got, want)
	}
	for _, name := range projectRemovalOrder {
		if a, ok := Lookup(name); !ok {
			t.Errorf("removal order names unknown agent %q", name)
		} else if _, ok := a.(ProjectInstaller); !ok {
			t.Errorf("removal order names %q, which has no project hooks", name)
		}
	}
}

func TestHookResponses(t *testing.T) {
	for _, tc := range []struct {
		agent, event, want string
		ok                 bool
	}{
		{"cursor", "beforeSubmitPrompt", `{"continue":true}`, true},
		{"cursor", "beforeReadFile", `{"permission":"allow"}`, true},
		{"cursor", "stop", "{}", true},
		{"agy", "Stop", `{"decision":"allow"}`, true},
		{"agy", "PreInvocation", "{}", true},
		{"gemini", "AfterTool", "{}", true},
		{"qwen", "Stop", "{}", true},
		{"codex", "Stop", "{}", true},
		{"codex", "PostToolUse", "", false},
		{"claude-code", "Stop", "", false},
		{"unknown", "Stop", "", false},
	} {
		t.Run(tc.agent+"/"+tc.event, func(t *testing.T) {
			a, _ := ForHook(tc.agent)
			got, ok := a.HookResponse(tc.event)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("HookResponse = %q, %t; want %q, %t", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestStartsDaemon(t *testing.T) {
	starts := map[string][]string{
		"copilot":   {"userPromptSubmitted"},
		"gemini":    {"BeforeAgent"},
		"cursor":    {"beforeSubmitPrompt"},
		"hermes":    {"pre_llm_call"},
		"openhands": {"user_prompt_submit"},
		"auggie":    {"PromptSubmit"},
		"cline":     {"TaskStart", "TaskResume"},
		"windsurf":  {"pre_user_prompt"},
	}
	events := []string{"userPromptSubmitted", "BeforeAgent", "beforeSubmitPrompt", "pre_llm_call", "user_prompt_submit", "PromptSubmit", "TaskStart", "TaskResume", "pre_user_prompt", "Stop"}
	for _, a := range All() {
		for _, ev := range events {
			want := slices.Contains(starts[a.Name()], ev)
			if got := a.StartsDaemon(ev); got != want {
				t.Errorf("%s.StartsDaemon(%q) = %t, want %t", a.Name(), ev, got, want)
			}
		}
	}
}

func TestHookCallReadsBodyOnce(t *testing.T) {
	c := NewHookCall("x", "x", "e", false, strings.NewReader("payload"))
	for i := 0; i < 2; i++ {
		b, err := c.Body()
		if err != nil || string(b) != "payload" {
			t.Fatalf("read %d: %q, %v", i, b, err)
		}
	}
}

// The README's integration table names the verified integrations.
func TestVerifiedAgentsMatchReadme(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range All() {
		if a.Name() == "git" {
			continue
		}
		caps := a.Capabilities()
		row := "| " + caps.DisplayName + " | Real "
		if got := strings.Contains(string(b), row); got != (caps.Verification == Verified) {
			t.Errorf("%s: verification %s, README row with live coverage present=%t", a.Name(), caps.Verification, got)
		}
	}
}
