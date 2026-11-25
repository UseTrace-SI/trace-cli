package export

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

var redactedMarker = regexp.MustCompile(`⟨redacted:([a-z-]+):[0-9a-f]+⟩`)

var (
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
	uuidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	gitSHA         = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// validate checks a decoded record against the JSON Schema published in
// section 6.1 of Agent Trace 0.1.0 (https://agent-trace.dev): required
// fields, types, enums, patterns and bounds.
func validate(raw []byte) error {
	var r map[string]any
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	for _, k := range []string{"version", "id", "timestamp", "files"} {
		if _, ok := r[k]; !ok {
			return fmt.Errorf("missing %s", k)
		}
	}
	if v, _ := r["version"].(string); !versionPattern.MatchString(v) {
		return fmt.Errorf("version %q", r["version"])
	}
	if v, _ := r["id"].(string); !uuidPattern.MatchString(v) {
		return fmt.Errorf("id %q", r["id"])
	}
	if v, _ := r["timestamp"].(string); func() error { _, err := time.Parse(time.RFC3339, v); return err }() != nil {
		return fmt.Errorf("timestamp %q", r["timestamp"])
	}
	if vcs, ok := r["vcs"]; ok {
		m, _ := vcs.(map[string]any)
		typ, _ := m["type"].(string)
		rev, _ := m["revision"].(string)
		if !map[string]bool{"git": true, "jj": true, "hg": true, "svn": true}[typ] || rev == "" || (typ == "git" && !gitSHA.MatchString(rev)) {
			return fmt.Errorf("vcs %v", vcs)
		}
	}
	if tool, ok := r["tool"]; ok {
		if _, ok := tool.(map[string]any); !ok {
			return fmt.Errorf("tool %v", tool)
		}
	}
	if md, ok := r["metadata"]; ok {
		if _, ok := md.(map[string]any); !ok {
			return fmt.Errorf("metadata %v", md)
		}
	}
	files, ok := r["files"].([]any)
	if !ok {
		return fmt.Errorf("files %v", r["files"])
	}
	for _, f := range files {
		file, _ := f.(map[string]any)
		if p, _ := file["path"].(string); p == "" {
			return fmt.Errorf("file path %v", file)
		}
		convs, ok := file["conversations"].([]any)
		if !ok {
			return fmt.Errorf("conversations %v", file)
		}
		for _, c := range convs {
			conv, _ := c.(map[string]any)
			if u, ok := conv["url"]; ok {
				s, _ := u.(string)
				if parsed, err := url.Parse(s); err != nil || parsed.Scheme == "" {
					return fmt.Errorf("url %v", u)
				}
			}
			if err := contributor(conv["contributor"], false); err != nil {
				return err
			}
			if rel, ok := conv["related"]; ok {
				list, ok := rel.([]any)
				if !ok {
					return fmt.Errorf("related %v", rel)
				}
				for _, item := range list {
					m, _ := item.(map[string]any)
					typ, _ := m["type"].(string)
					u, _ := m["url"].(string)
					if parsed, err := url.Parse(u); typ == "" || err != nil || parsed.Scheme == "" {
						return fmt.Errorf("related %v", item)
					}
				}
			}
			ranges, ok := conv["ranges"].([]any)
			if !ok {
				return fmt.Errorf("ranges %v", conv)
			}
			for _, rg := range ranges {
				m, _ := rg.(map[string]any)
				s, ok1 := m["start_line"].(float64)
				e, ok2 := m["end_line"].(float64)
				if !ok1 || !ok2 || s < 1 || e < 1 || s != float64(int(s)) || e != float64(int(e)) {
					return fmt.Errorf("range %v", rg)
				}
				if err := contributor(m["contributor"], false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func contributor(v any, required bool) error {
	if v == nil {
		if required {
			return fmt.Errorf("missing contributor")
		}
		return nil
	}
	m, _ := v.(map[string]any)
	typ, _ := m["type"].(string)
	if !map[string]bool{"human": true, "ai": true, "mixed": true, "unknown": true}[typ] {
		return fmt.Errorf("contributor type %v", v)
	}
	if id, ok := m["model_id"]; ok {
		s, ok := id.(string)
		if !ok || len(s) > 250 {
			return fmt.Errorf("model_id %v", id)
		}
	}
	return nil
}

func TestValidatorRejectsInvalid(t *testing.T) {
	for _, raw := range []string{
		`{"version":"0.1.0","id":"00000000-0000-4000-8000-000000000000","timestamp":"2026-01-23T14:30:00Z","files":[]}`,
		`{"version":"0.1","id":"x","timestamp":"2026-01-23T14:30:00Z","files":[]}`,
		`{"version":"0.1","id":"00000000-0000-4000-8000-000000000000","timestamp":"2026-01-23T14:30:00Z","files":[{"path":"a","conversations":[{"contributor":{"type":"bot"},"ranges":[]}]}]}`,
		`{"version":"0.1","id":"00000000-0000-4000-8000-000000000000","timestamp":"2026-01-23T14:30:00Z","files":[{"path":"a","conversations":[{"ranges":[{"start_line":0,"end_line":1}]}]}]}`,
	} {
		if validate([]byte(raw)) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	minimal := `{"version":"0.1","id":"00000000-0000-4000-8000-000000000000","timestamp":"2026-01-23T14:30:00Z","files":[{"path":"a","conversations":[{"contributor":{"type":"ai"},"ranges":[{"start_line":1,"end_line":50}]}]}]}`
	if err := validate([]byte(minimal)); err != nil {
		t.Error(err)
	}
}

func TestFileRecordGroupsRanges(t *testing.T) {
	lines := []Line{{Agent: "codex", TurnID: "t1"}, {Agent: "codex", TurnID: "t1"}, {Agent: "human"}, {Agent: "codex", TurnID: "t1"}, {Agent: "baseline"}, {Agent: "claude-code", TurnID: "t2"}}
	f := FileRecord("a.go", 3, lines, nil)
	got, _ := json.Marshal(f)
	want := `{"path":"a.go","conversations":[{"url":"trace://turn/t1","contributor":{"type":"ai"},"ranges":[{"start_line":3,"end_line":4},{"start_line":6,"end_line":6}]},{"contributor":{"type":"human"},"ranges":[{"start_line":5,"end_line":5}]},{"contributor":{"type":"unknown"},"ranges":[{"start_line":7,"end_line":7}]},{"url":"trace://turn/t2","contributor":{"type":"ai"},"ranges":[{"start_line":8,"end_line":8}]}]}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T",
		"GIT_COMMITTER_EMAIL=t@example.com", "GIT_AUTHOR_DATE=2026-01-23T14:30:00Z", "GIT_COMMITTER_DATE=2026-01-23T14:30:00Z",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func fixture(t *testing.T) (string, *store.DB, *blob.Store) {
	root := t.TempDir()
	run(t, root, "git", "init", "-q", "-b", "main")
	db, err := store.Open(filepath.Join(root, ".trace", "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bs, err := blob.Open(filepath.Join(root, ".trace", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"f.txt": "header\nagent one\nagent two\nfooter\n", ".env": "TOKEN=sk-secret\n", "secret/key.txt": "hidden\n"}
	put := func(s string) string {
		h, err := bs.PutBytes([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return string(h)
	}
	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO sessions(id,agent,worktree,started_at) VALUES('s','codex',?,1)", root)
	exec("INSERT INTO turns(id,session_id,seq,prompt,started_at) VALUES('t1','s',1,'edit',1)")
	exec("INSERT INTO ops(id,turn_id,path,kind,before_hash,after_hash,source,at) VALUES('o1','t1','f.txt','modify',?,?,'tool',2)", put("header\nfooter\n"), put(files["f.txt"]))
	for i, p := range []string{".env", "secret/key.txt"} {
		exec("INSERT INTO ops(id,turn_id,path,kind,before_hash,after_hash,source,at) VALUES(?,'t1',?,'create',NULL,?,'tool',3)", fmt.Sprintf("x%d", i), p, put(files[p]))
	}
	for p, s := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".traceignore"), []byte("secret/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, db, bs
}

func TestWorkingTreeRecord(t *testing.T) {
	root, db, bs := fixture(t)
	r, err := WorkingTreeRecord(db, bs, root, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if err := validate(b); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if r.VCS != nil || len(r.Files) != 1 || r.Files[0].Path != "f.txt" {
		t.Fatalf("record: %s", b)
	}
	if bytes.Contains(b, []byte(".env")) || bytes.Contains(b, []byte("secret")) || bytes.Contains(b, []byte("hidden")) {
		t.Fatalf("excluded path leaked: %s", b)
	}
	convs := r.Files[0].Conversations
	if convs[0].Contributor.Type != "unknown" || convs[1].URL != "trace://turn/t1" || convs[1].Ranges[0] != (Range{2, 3}) {
		t.Fatalf("conversations: %s", b)
	}
}

func TestCommitRecordGolden(t *testing.T) {
	root, db, bs := fixture(t)
	run(t, root, "git", "add", "-f", "f.txt", ".env", "secret/key.txt")
	run(t, root, "git", "commit", "-q", "-m", "work")
	shas, err := Commits(root, "HEAD")
	if err != nil || len(shas) != 1 {
		t.Fatalf("commits %v %v", shas, err)
	}
	r, err := CommitRecord(db, bs, root, shas[0])
	if err != nil {
		t.Fatal(err)
	}
	again, err := CommitRecord(db, bs, root, shas[0])
	if err != nil || again.ID != r.ID {
		t.Fatalf("export is not stable: %v", err)
	}
	var b bytes.Buffer
	if err := WriteJSONL(&b, []Record{r}); err != nil {
		t.Fatal(err)
	}
	if err := validate(b.Bytes()); err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(b.String(), shas[0], "<sha>")
	golden := filepath.Join("testdata", "commit.agent-trace.jsonl")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	// The record ID derives from the SHA, which differs across git versions'
	// object formats only if the fixture changes; compare everything else.
	idPattern := regexp.MustCompile(`"id":"[^"]+"`)
	if idPattern.ReplaceAllString(got, `"id":""`) != idPattern.ReplaceAllString(string(want), `"id":""`) {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestCommitRecordUsesImportedHints(t *testing.T) {
	root, db, bs := fixture(t)
	run(t, root, "git", "add", "f.txt")
	run(t, root, "git", "commit", "-q", "-m", "work")
	sha := run(t, root, "git", "rev-parse", "HEAD")
	// Line 1 has no Trace origin; line 2 is a recorded turn and keeps it.
	if _, err := db.Exec(`INSERT INTO imported_attributions(source,commit_sha,path,start_line,end_line,contributor,tool,model,conversation,imported_at)
		VALUES('git-ai',?,'f.txt',1,2,'ai','cursor','claude-sonnet-4-5','s_1',1)`, sha); err != nil {
		t.Fatal(err)
	}
	r, err := CommitRecord(db, bs, root, sha)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if err := validate(b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `{"contributor":{"type":"ai","model_id":"anthropic/claude-sonnet-4-5"},"ranges":[{"start_line":1,"end_line":1}],"related":[{"type":"git-ai.session","url":"urn:git-ai:session:s_1"}]}`) ||
		!strings.Contains(string(b), `"url":"trace://turn/t1","contributor":{"type":"ai"},"ranges":[{"start_line":2,"end_line":3}]`) ||
		!strings.Contains(string(b), `"imported_hint_lines":{"git-ai":1}`) ||
		!strings.Contains(string(b), `"urn:git-ai:session:s_1":{"model":"claude-sonnet-4-5","session":"s_1","source":"git-ai","tool":"cursor"}`) {
		t.Fatalf("record: %s", b)
	}
}

func TestModelID(t *testing.T) {
	for in, want := range map[string]string{
		"claude-sonnet-4-5-20250514": "anthropic/claude-sonnet-4-5-20250514",
		"gpt-5":                      "openai/gpt-5",
		"o3-mini":                    "openai/o3-mini",
		"gemini-2.5-pro":             "google/gemini-2.5-pro",
		"anthropic/claude-opus-4-5":  "anthropic/claude-opus-4-5",
		"llama-3":                    "",
		"opus":                       "",
		"a/b/c":                      "",
		"":                           "",
	} {
		if got := ModelID(in); got != want {
			t.Errorf("ModelID(%q) = %q; want %q", in, got, want)
		}
	}
}
