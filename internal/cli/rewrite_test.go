package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/gitx"
)

func (r *rewriteRepo) why(path, text string) whyResult {
	r.t.Helper()
	line := slices.Index(strings.Split(r.read(path), "\n"), text) + 1
	if line < 1 {
		r.t.Fatalf("%q not found in %s:\n%s", text, path, r.read(path))
	}
	jsonOutput := true
	cmd := whyCommand(&r.root, &jsonOutput)
	cmd.SetArgs([]string{fmt.Sprintf("%s:%d", path, line)})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		r.t.Fatal(err)
	}
	var results []whyResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil || len(results) == 0 {
		r.t.Fatalf("why output %q: %v", out.String(), err)
	}
	return results[0]
}

type lineCheck struct {
	path, text string
	agent      string // "codex", "human", "baseline" or "unknown"
	prompt     string
	confidence string
	rewrites   []string // kinds along the rewrite chain, oldest first
}

func (r *rewriteRepo) check(checks []lineCheck) {
	r.t.Helper()
	r.hookErrors()
	head := r.git("rev-parse", "HEAD")
	for _, c := range checks {
		got := r.why(c.path, c.text)
		var kinds []string
		for _, rewrite := range got.RewriteChain {
			kinds = append(kinds, rewrite.Kind)
		}
		if got.Agent != c.agent || got.Prompt != c.prompt || got.Confidence != c.confidence || !slices.Equal(kinds, c.rewrites) {
			b, _ := json.Marshal(got)
			r.t.Errorf("%s %q: got agent=%s prompt=%q confidence=%s rewrites=%v (%s); want %s %q %s %v",
				c.path, c.text, got.Agent, got.Prompt, got.Confidence, kinds, b, c.agent, c.prompt, c.confidence, c.rewrites)
			continue
		}
		if n := len(got.RewriteChain); n > 0 {
			// A line may reach the first rewritten commit through ordinary
			// history, so only the end of the chain is fixed.
			if got.RewriteChain[n-1].To != got.Commit || got.OriginCommit == "" {
				r.t.Errorf("%s %q: chain %+v does not end at %s (origin %q)", c.path, c.text, got.RewriteChain, got.Commit, got.OriginCommit)
			}
			if !gitx.IsAncestor(r.root, got.Commit, head) {
				r.t.Errorf("%s %q: reported commit %s is not in the current history", c.path, c.text, got.Commit)
			}
		}
	}
}

func TestWhySurvivesGitRewrites(t *testing.T) {
	for _, tc := range []struct {
		name  string
		needs string
		run   func(r *rewriteRepo) []lineCheck
	}{
		{"amend message", "", func(r *rewriteRepo) []lineCheck {
			r.agentTurn("add Agent", map[string]string{"app.go": baseGo + "\nfunc Agent() int {\n\treturn 2\n}\n"})
			r.git("add", "-A")
			r.git("commit", "-q", "-m", "agent")
			r.git("commit", "-q", "--amend", "-m", "agent, reworded")
			return []lineCheck{
				{"app.go", "\treturn 2", "codex", "add Agent", "exact", []string{"amend"}},
				{"app.go", "\treturn 1", "baseline", "", "exact", nil},
			}
		}},
		{"amend with a human change", "", func(r *rewriteRepo) []lineCheck {
			r.agentTurn("add Agent", map[string]string{"app.go": baseGo + "\nfunc Agent() int {\n\treturn 2\n}\n"})
			r.git("add", "-A")
			r.git("commit", "-q", "-m", "agent")
			r.write("app.go", r.read("app.go")+"\n// reviewed by a human\n")
			r.watch("app.go")
			r.git("commit", "-q", "-a", "--amend", "--no-edit")
			return []lineCheck{
				{"app.go", "func Agent() int {", "codex", "add Agent", "exact", []string{"amend"}},
				{"app.go", "// reviewed by a human", "human", "", "exact", nil},
			}
		}},
		{"interactive rebase squash", "", func(r *rewriteRepo) []lineCheck {
			base := r.git("rev-parse", "HEAD")
			r.agentTurn("first", map[string]string{"app.go": baseGo + "\nfunc First() {}\n"})
			r.git("commit", "-q", "-a", "-m", "first")
			first := r.git("rev-parse", "HEAD")
			r.agentTurn("second", map[string]string{"app.go": baseGo + "\nfunc First() {}\n\nfunc Second() {}\n"})
			r.git("commit", "-q", "-a", "-m", "second")
			second := r.git("rev-parse", "HEAD")
			r.write("notes.txt", "written by a human\n")
			r.watch("notes.txt")
			r.git("add", "notes.txt")
			r.git("commit", "-q", "-m", "notes")
			notes := r.git("rev-parse", "HEAD")
			r.sequence(base, "pick "+first, "squash "+second, "pick "+notes)
			return []lineCheck{
				{"app.go", "func First() {}", "codex", "first", "exact", []string{"squash"}},
				{"app.go", "func Second() {}", "codex", "second", "exact", []string{"squash"}},
				{"notes.txt", "written by a human", "human", "", "exact", []string{"rebase"}},
			}
		}},
		{"interactive rebase reorder", "", func(r *rewriteRepo) []lineCheck {
			base := r.git("rev-parse", "HEAD")
			r.agentTurn("agent file", map[string]string{"agent.go": "package app\n\nfunc FromAgent() {}\n"})
			r.git("add", "-A")
			r.git("commit", "-q", "-m", "agent")
			agent := r.git("rev-parse", "HEAD")
			r.write("human.go", "package app\n\nfunc FromHuman() {}\n")
			r.watch("human.go")
			r.git("add", "-A")
			r.git("commit", "-q", "-m", "human")
			human := r.git("rev-parse", "HEAD")
			r.sequence(base, "pick "+human, "pick "+agent)
			return []lineCheck{
				{"agent.go", "func FromAgent() {}", "codex", "agent file", "exact", []string{"rebase"}},
				{"human.go", "func FromHuman() {}", "human", "", "exact", []string{"rebase"}},
			}
		}},
		{"rebase onto upstream", "", func(r *rewriteRepo) []lineCheck {
			r.git("checkout", "-q", "-b", "feature")
			r.agentTurn("feature", map[string]string{"app.go": baseGo + "\nfunc Feature() {}\n"})
			r.git("commit", "-q", "-a", "-m", "feature")
			r.git("checkout", "-q", "main")
			r.watch("app.go")
			r.write("app.go", "// Package app is shared.\n"+r.read("app.go"))
			r.watch("app.go")
			r.git("commit", "-q", "-a", "-m", "doc")
			r.git("checkout", "-q", "feature")
			r.watch("app.go")
			r.git("rebase", "-q", "main")
			r.watch("app.go")
			return []lineCheck{
				{"app.go", "func Feature() {}", "codex", "feature", "exact", []string{"rebase"}},
				{"app.go", "// Package app is shared.", "human", "", "exact", nil},
			}
		}},
		{"cherry-pick after Git removed and restored the lines", "", func(r *rewriteRepo) []lineCheck {
			r.git("checkout", "-q", "-b", "topic")
			r.agentTurn("topic change", map[string]string{"util.go": "package app\n\nfunc Util() {}\n"})
			r.git("add", "-A")
			r.git("commit", "-q", "-m", "util")
			topic := r.git("rev-parse", "HEAD")
			r.git("checkout", "-q", "main")
			r.watch("util.go")
			r.write("readme.txt", "main moved on\n")
			r.watch("readme.txt")
			r.git("add", "readme.txt")
			r.git("commit", "-q", "-m", "readme")
			r.git("cherry-pick", topic)
			r.watch("util.go")
			return []lineCheck{{"util.go", "func Util() {}", "codex", "topic change", "exact", []string{"cherry-pick"}}}
		}},
		{"squash merge", "", func(r *rewriteRepo) []lineCheck {
			r.git("checkout", "-q", "-b", "feat")
			r.agentTurn("feat one", map[string]string{"lib.go": "package app\n\nfunc One() {}\n"})
			r.git("add", "-A")
			r.git("commit", "-q", "-m", "one")
			r.agentTurn("feat two", map[string]string{"lib.go": "package app\n\nfunc One() {}\n\nfunc Two() {}\n"})
			r.git("commit", "-q", "-a", "-m", "two")
			r.git("checkout", "-q", "main")
			r.watch("lib.go")
			r.git("merge", "-q", "--squash", "feat")
			r.watch("lib.go")
			r.git("commit", "-q", "--no-edit")
			return []lineCheck{
				{"lib.go", "func One() {}", "codex", "feat one", "exact", []string{"merge-squash"}},
				{"lib.go", "func Two() {}", "codex", "feat two", "exact", []string{"merge-squash"}},
			}
		}},
		{"squash merge with an edited message", "", func(r *rewriteRepo) []lineCheck {
			// The squashed file equals the feature tip, a state already recorded.
			r.git("checkout", "-q", "-b", "feat")
			r.agentTurn("feat one", map[string]string{"lib.go": "package app\n\nfunc OneFeature() {}\n"})
			r.git("add", "-A")
			r.git("commit", "-q", "-m", "one")
			r.agentTurn("feat two", map[string]string{"lib.go": "package app\n\nfunc OneFeature() {}\n\nfunc TwoFeature() {}\n"})
			r.git("commit", "-q", "-a", "-m", "two")
			r.git("checkout", "-q", "main")
			r.watch("lib.go")
			r.git("merge", "-q", "--squash", "feat")
			r.watch("lib.go")
			r.git("commit", "-q", "-m", "Add the library")
			return []lineCheck{
				{"lib.go", "func OneFeature() {}", "codex", "feat one", "exact", nil},
				{"lib.go", "func TwoFeature() {}", "codex", "feat two", "exact", nil},
			}
		}},
		{"squash merge with an edited message after main changed the file", "", func(r *rewriteRepo) []lineCheck {
			// No hook names the squashed commits and the merged file is new
			// content, so lines are matched to the feature commits by content.
			r.git("checkout", "-q", "-b", "feat")
			r.agentTurn("feat one", map[string]string{"app.go": baseGo + "\nfunc OneFeature() int {\n\treturn 10\n}\n"})
			r.git("commit", "-q", "-a", "-m", "one")
			r.agentTurn("feat two", map[string]string{"app.go": baseGo + "\nfunc OneFeature() int {\n\treturn 10\n}\n\nfunc TwoFeature() int {\n\treturn 20\n}\n"})
			r.git("commit", "-q", "-a", "-m", "two")
			r.git("checkout", "-q", "main")
			r.watch("app.go")
			r.write("app.go", "// Package app is shared.\n"+r.read("app.go"))
			r.watch("app.go")
			r.git("commit", "-q", "-a", "-m", "doc")
			r.git("merge", "-q", "--squash", "feat")
			r.watch("app.go")
			r.git("commit", "-q", "-m", "Add features")
			return []lineCheck{
				{"app.go", "func OneFeature() int {", "codex", "feat one", "inferred", []string{"inferred"}},
				{"app.go", "func TwoFeature() int {", "codex", "feat two", "inferred", []string{"inferred"}},
				{"app.go", "// Package app is shared.", "human", "", "exact", nil},
			}
		}},
		{"gofmt reformat", "gofmt", func(r *rewriteRepo) []lineCheck {
			r.agentTurn("add Sum", map[string]string{"app.go": baseGo + "\nfunc Sum(a, b int) int {\n    total := a+b\n    return total\n}\n"})
			r.git("commit", "-q", "-a", "-m", "sum")
			if out, err := exec.Command("gofmt", "-w", filepath.Join(r.root, "app.go")).CombinedOutput(); err != nil {
				r.t.Fatalf("gofmt: %v: %s", err, out)
			}
			r.watch("app.go")
			r.git("commit", "-q", "-a", "-m", "fmt")
			return []lineCheck{
				{"app.go", "\ttotal := a + b", "codex", "add Sum", "normalized", nil},
				{"app.go", "func Sum(a, b int) int {", "codex", "add Sum", "exact", nil},
			}
		}},
		{"prettier-style reformat", "", func(r *rewriteRepo) []lineCheck {
			r.agentTurn("add greet", map[string]string{"web.js": "function greet(name) {\n  return 'hello ' + name\n}\n" +
				"const names = ['ada', 'grace']\nconsole.log(greet(names[0]), greet(names[1]), 'and many more names here')\n"})
			r.git("add", "-A")
			r.git("commit", "-q", "-m", "greet")
			r.write("web.js", "function greet(name) {\n    return \"hello \" + name;\n}\nconst names = [\"ada\", \"grace\"];\n"+
				"console.log(\n    greet(names[0]),\n    greet(names[1]),\n    \"and many more names here\",\n);\n")
			r.watch("web.js")
			r.git("commit", "-q", "-a", "-m", "prettier")
			return []lineCheck{
				{"web.js", "    return \"hello \" + name;", "codex", "add greet", "normalized", nil},
				{"web.js", "const names = [\"ada\", \"grace\"];", "codex", "add greet", "normalized", nil},
				{"web.js", "    greet(names[1]),", "codex", "add greet", "normalized", nil},
				{"web.js", "function greet(name) {", "codex", "add greet", "exact", nil},
			}
		}},
		{"unrecorded line stays uncertain", "", func(r *rewriteRepo) []lineCheck {
			r.agentTurn("add X", map[string]string{"app.go": baseGo + "\nfunc X() {}\n"})
			r.git("commit", "-q", "-a", "-m", "x")
			r.write("app.go", r.read("app.go")+"\nfunc Unrecorded() {}\n")
			r.git("-c", "core.hooksPath="+os.DevNull, "commit", "-q", "-a", "-m", "unobserved")
			return []lineCheck{
				{"app.go", "func Unrecorded() {}", "unknown", "", "uncertain", nil},
				{"app.go", "func X() {}", "codex", "add X", "exact", nil},
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needs != "" {
				if _, err := exec.LookPath(tc.needs); err != nil {
					t.Skipf("%s is not installed", tc.needs)
				}
			}
			r := newRewriteRepo(t, map[string]string{"app.go": baseGo})
			r.check(tc.run(r))
		})
	}
}

func TestBlameSummarizesLines(t *testing.T) {
	r := newRewriteRepo(t, map[string]string{"app.go": baseGo})
	r.agentTurn("add Agent", map[string]string{"app.go": baseGo + "\nfunc Agent() int {\n\treturn 2\n}\n"})
	r.git("commit", "-q", "-a", "-m", "agent")
	r.git("commit", "-q", "--amend", "-m", "agent, reworded")
	r.write("app.go", r.read("app.go")+"\n// uncommitted note\n")
	r.watch("app.go")
	r.hookErrors()
	jsonOutput := true
	cmd := blameCommand(&r.root, &jsonOutput)
	cmd.SetArgs([]string{"app.go"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var report blameReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(r.read("app.go"), "\n"), "\n")
	if report.File != "app.go" || len(report.Lines) != len(lines) {
		t.Fatalf("blame report covers %d of %d lines: %s", len(report.Lines), len(lines), out.String())
	}
	want := map[string]string{"package app": "baseline", "\treturn 1": "baseline", "func Agent() int {": "codex",
		"\treturn 2": "codex", "// uncommitted note": "human"}
	for i, text := range lines {
		agent, ok := want[text]
		if !ok {
			continue
		}
		got := report.Lines[i]
		if got.Agent != agent {
			t.Errorf("line %d %q: %+v; want %s", i+1, text, got, agent)
		}
		if agent == "codex" && (got.Step < 1 || len(got.RewriteChain) != 1 || got.RewriteChain[0].Kind != "amend" || got.Commit == "") {
			t.Errorf("line %d %q: agent line lacks its step or rewrite chain: %+v", i+1, text, got)
		}
		if text == "// uncommitted note" && got.Commit != "" {
			t.Errorf("uncommitted line reported commit %s", got.Commit)
		}
	}
	if report.Summary["codex"] != 4 || report.Summary["human"] != 2 || report.Summary["baseline"] != 5 {
		t.Fatalf("summary = %v", report.Summary)
	}
	jsonOutput = false
	cmd = blameCommand(&r.root, &jsonOutput)
	cmd.SetArgs([]string{"app.go"})
	out.Reset()
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "codex #") || !strings.Contains(out.String(), "| func Agent() int {") {
		t.Fatalf("text blame = %s", out.String())
	}
	cmd = blameCommand(&r.root, &jsonOutput)
	cmd.SetArgs([]string{".env"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("blame read a secret path")
	}
}
