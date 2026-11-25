//go:build !windows

package main

// The golden tests run the real trace binary as agents and users do and
// compare what it prints, its exit status and every file it writes with
// recorded results in testdata/golden. They pin the observable behaviour of
// each agent integration: hook responses, spool frames, daemon start-up and
// the configuration written by init, agent add/remove, uninstall and setup.
//
// Regenerate after an intended behaviour change with
//
//	go test ./cmd/trace -run Golden -update-golden
//
// and review the diff of testdata/golden like any other change.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/golden from the current binary")

// result is what one invocation of the binary did.
type result struct {
	Args   []string          `json:"args"`
	Stdout string            `json:"stdout"`
	Stderr string            `json:"stderr,omitempty"`
	Exit   int               `json:"exit"`
	Files  map[string]string `json:"files,omitempty"`
}

type golden struct {
	t       *testing.T
	name    string
	results map[string]result
}

func newGolden(t *testing.T, name string) *golden {
	return &golden{t: t, name: name, results: map[string]result{}}
}

func (g *golden) add(key string, r result) {
	g.t.Helper()
	if _, ok := g.results[key]; ok {
		g.t.Fatalf("duplicate golden case %q", key)
	}
	g.results[key] = r
}

// check compares every case with the recorded file, reporting each mismatch.
func (g *golden) check() {
	t := g.t
	t.Helper()
	path := filepath.Join("testdata", "golden", g.name+".json")
	if *updateGolden {
		b, err := json.MarshalIndent(g.results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s (run with -update-golden to create it): %v", path, err)
	}
	var want map[string]result
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range g.results {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	failures := 0
	for _, k := range names {
		w, inWant := want[k]
		got, inGot := g.results[k]
		switch {
		case !inWant:
			t.Errorf("%s: case %q is not recorded", g.name, k)
		case !inGot:
			t.Errorf("%s: recorded case %q did not run", g.name, k)
		default:
			diff := describeDiff(w, got)
			if diff == "" {
				continue
			}
			t.Errorf("%s: case %q differs:\n%s", g.name, k, diff)
		}
		if failures++; failures >= 20 {
			t.Fatalf("%s: stopping after %d differences", g.name, failures)
		}
	}
}

// describeDiff lists the fields and files that differ between two results.
func describeDiff(want, got result) string {
	var b strings.Builder
	field := func(name, w, g string) {
		if w != g {
			fmt.Fprintf(&b, "  %s:\n    want %q\n    got  %q\n", name, w, g)
		}
	}
	field("args", strings.Join(want.Args, " "), strings.Join(got.Args, " "))
	field("stdout", want.Stdout, got.Stdout)
	field("stderr", want.Stderr, got.Stderr)
	field("exit", strconv.Itoa(want.Exit), strconv.Itoa(got.Exit))
	paths := map[string]bool{}
	for p := range want.Files {
		paths[p] = true
	}
	for p := range got.Files {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		w, inWant := want.Files[p]
		g, inGot := got.Files[p]
		if !inWant {
			w = "<absent>"
		}
		if !inGot {
			g = "<absent>"
		}
		field("file "+p, w, g)
	}
	return b.String()
}

// sandbox is a scratch user and repository layout; nothing outside it is
// read or written by the binary under test.
type sandbox struct {
	t         *testing.T
	bin       string
	base      string
	home      string
	traceHome string
	pathDir   string
	replacer  *strings.Replacer
}

var traceBin string

func TestMain(m *testing.M) {
	flag.Parse()
	dir, err := os.MkdirTemp("", "trace-golden-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	traceBin = filepath.Join(dir, "trace")
	cmd := exec.Command("go", "build", "-o", traceBin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build: %s: %v\n", out, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	base := t.TempDir()
	s := &sandbox{t: t, bin: traceBin, base: base, home: filepath.Join(base, "home"), traceHome: filepath.Join(base, "tracehome"), pathDir: filepath.Join(base, "bin")}
	for _, dir := range []string{s.home, s.traceHome, s.pathDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Only git is on PATH, so agent detection and version probes are the
	// same on every machine.
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	if err := os.Symlink(git, filepath.Join(s.pathDir, "git")); err != nil {
		t.Fatal(err)
	}
	var pairs []string
	add := func(path, placeholder string) {
		if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
			pairs = append(pairs, real, placeholder)
		}
		pairs = append(pairs, path, placeholder)
	}
	// Longer paths first so a prefix never replaces part of a longer one.
	add(s.bin, "{{BIN}}")
	add(s.home, "{{HOME}}")
	add(s.traceHome, "{{TRACE_HOME}}")
	add(s.pathDir, "{{PATH}}")
	add(base, "{{BASE}}")
	s.replacer = strings.NewReplacer(pairs...)
	return s
}

func (s *sandbox) env(extra map[string]string) []string {
	env := map[string]string{
		"HOME":                s.home,
		"TRACE_HOME":          s.traceHome,
		"PATH":                s.pathDir,
		"LANG":                "C",
		"LC_ALL":              "C",
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "Trace Test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "Trace Test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
		"GIT_AUTHOR_DATE":     "2026-01-01T00:00:00Z",
		"GIT_COMMITTER_DATE":  "2026-01-01T00:00:00Z",
		"TMPDIR":              filepath.Join(s.base, "tmp"),
	}
	_ = os.MkdirAll(env["TMPDIR"], 0700)
	for k, v := range extra {
		if v == "" {
			delete(env, k)
		} else {
			env[k] = v
		}
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func (s *sandbox) expand(v string) string {
	return strings.NewReplacer("{{HOME}}", s.home, "{{BASE}}", s.base, "{{TRACE_HOME}}", s.traceHome).Replace(v)
}

var atPattern = regexp.MustCompile(`"at":([0-9]{13})\b`)

// normalize replaces sandbox paths and the current time, which differ per run.
func (s *sandbox) normalize(v string, since time.Time) string {
	v = s.replacer.Replace(v)
	lo, hi := since.Add(-2*time.Second).UnixMilli(), time.Now().Add(2*time.Second).UnixMilli()
	return atPattern.ReplaceAllStringFunc(v, func(m string) string {
		n, err := strconv.ParseInt(m[len(`"at":`):], 10, 64)
		if err == nil && n >= lo && n <= hi {
			return `"at":"{{NOW}}"`
		}
		return m
	})
}

func (s *sandbox) git(dir string, args ...string) {
	s.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = s.env(nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		s.t.Fatalf("git %v: %s: %v", args, out, err)
	}
}

// repo creates a committed Git repository with one source file and a
// subdirectory.
func (s *sandbox) repo(name string) string {
	s.t.Helper()
	root := filepath.Join(s.base, name)
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0755); err != nil {
		s.t.Fatal(err)
	}
	s.git(root, "init", "-q")
	writeFile(s.t, filepath.Join(root, "a.go"), "package a\n")
	writeFile(s.t, filepath.Join(root, "sub", "b.go"), "package sub\n")
	s.git(root, "add", "-A")
	s.git(root, "commit", "-q", "-m", "init")
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

type invocation struct {
	dir   string
	args  []string
	stdin string
	env   map[string]string
}

func (s *sandbox) run(inv invocation) result {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.bin, inv.args...)
	cmd.Dir = inv.dir
	cmd.Env = s.env(inv.env)
	cmd.Stdin = strings.NewReader(inv.stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	started := time.Now()
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		s.t.Fatalf("running %v: %v", inv.args, err)
	}
	args := make([]string, len(inv.args))
	for i, a := range inv.args {
		args[i] = s.normalize(a, started)
	}
	return result{Args: args, Stdout: s.normalize(stdout.String(), started), Stderr: s.normalize(stderr.String(), started), Exit: code}
}

// tree records every file and directory under the named roots, with mode
// and content. Volatile Trace internals are reduced to placeholders.
func (s *sandbox) tree(roots map[string]string, since time.Time) map[string]string {
	s.t.Helper()
	out := map[string]string{}
	for label, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if rel == "." || rel == ".git" {
				return nil
			}
			if skip, placeholder := volatile(rel); skip {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			} else if placeholder != "" {
				out[label+"/"+rel] = placeholder
				return nil
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			key := label + "/" + rel
			mode := fmt.Sprintf("[%04o]", info.Mode().Perm())
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, _ := os.Readlink(path)
				out[key] = "[symlink] " + s.normalize(target, since)
			case d.IsDir():
				out[key+"/"] = mode + " dir"
			default:
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if bytes.HasPrefix(b, []byte("PK\x03\x04")) {
					out[key] = mode + " " + s.zipListing(b, since)
				} else if utf8.Valid(b) && !bytes.ContainsRune(b, 0) {
					out[key] = mode + " " + s.normalize(string(b), since)
				} else {
					out[key] = fmt.Sprintf("%s sha256:%x (%d bytes)", mode, sha256.Sum256(b), len(b))
				}
			}
			return nil
		})
		if err != nil {
			s.t.Fatal(err)
		}
	}
	return out
}

// zipListing describes an archive by its entries, whose text embeds
// sandbox paths, so compressed bytes differ between runs.
func (s *sandbox) zipListing(b []byte, since time.Time) string {
	r, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return fmt.Sprintf("invalid zip: %v", err)
	}
	var out strings.Builder
	out.WriteString("zip")
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			return fmt.Sprintf("invalid zip entry %s: %v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return fmt.Sprintf("invalid zip entry %s: %v", f.Name, err)
		}
		fmt.Fprintf(&out, "\n== %s [%04o]\n%s", f.Name, f.Mode().Perm(), s.normalize(string(content), since))
	}
	return out.String()
}

// volatile reports Trace internals whose content is random or time-based.
func volatile(rel string) (skip bool, placeholder string) {
	base := filepath.Base(rel)
	switch {
	case strings.HasPrefix(rel, ".git/") && !strings.HasPrefix(rel, ".git/hooks") && rel != ".git/info" && rel != ".git/info/exclude":
		return true, ""
	case strings.HasPrefix(rel, ".git/hooks/") && strings.HasSuffix(rel, ".sample"):
		return true, ""
	case strings.HasPrefix(rel, ".trace/objects/"):
		// Object names hash file contents that embed sandbox paths.
		return true, ""
	case rel == ".trace/objects":
		return false, "{{OBJECTS}}"
	case strings.HasPrefix(base, "trace.db"):
		return false, "{{DATABASE}}"
	case base == "redaction.key" || base == "redaction.key.lock":
		return false, "{{KEY}}"
	case strings.HasSuffix(base, "installed-sha"):
		return false, "{{SHA}}"
	}
	return false, ""
}

// changes returns files that are new or changed in after, and files that
// disappeared.
func changes(before, after map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range after {
		if old, ok := before[k]; !ok || old != v {
			out[k] = v
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			out[k] = "<deleted>"
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// holdFakeDaemon makes root look like it has a running daemon: the daemon
// lock is held by this test process and spool.ready exists. init and agent
// commands then never start a real daemon.
func holdFakeDaemon(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".trace")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spool.ready"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
}

// traceDaemons returns the PIDs of daemons the binary under test runs for
// root, or for any repository when root is empty. A started daemon runs
// detached, so the tests find it by its command line.
func traceDaemons(t *testing.T, root string) []int {
	t.Helper()
	bins := map[string]bool{traceBin: true}
	if real, err := filepath.EvalSymlinks(traceBin); err == nil {
		bins[real] = true
	}
	roots := map[string]bool{root: true}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		roots[real] = true
	}
	matches := func(args []string) bool {
		n := len(args)
		return n >= 4 && bins[args[0]] && args[n-1] == "daemon" && (root == "" || (args[n-3] == "--repo" && roots[args[n-2]]))
	}
	var pids []int
	if entries, err := os.ReadDir("/proc"); err == nil {
		for _, entry := range entries {
			pid, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}
			b, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
			if err == nil && matches(strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")) {
				pids = append(pids, pid)
			}
		}
		return pids
	}
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "args=").Output()
	if err != nil {
		t.Fatalf("listing processes: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Sandbox paths contain no spaces, so the fields are the arguments.
		if pid, err := strconv.Atoi(fields[0]); err == nil && matches(fields[1:]) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// waitForDaemons waits until no daemon of the binary under test runs for
// root ("" for any), first asking them to stop when stop is set.
func waitForDaemons(t *testing.T, root string, stop bool) {
	t.Helper()
	if stop {
		for _, pid := range traceDaemons(t, root) {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for len(traceDaemons(t, root)) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("a Trace daemon for %q is still running", root)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
