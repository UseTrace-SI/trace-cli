package export

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/attribution"
	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/gitx"
	"github.com/UseTrace-SI/trace-cli/internal/interop"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

// applyHints explains lines Trace has no origin for (unknown or baseline)
// from imported git-ai hints for the same commit and path.
func applyHints(db attribution.Querier, sha, path string, lines []int, out []Line) (int, error) {
	hints, err := interop.HintsFor(db, sha, path)
	if err != nil || len(hints) == 0 {
		return 0, err
	}
	n := 0
	for i, line := range lines {
		if contributorType(out[i]) != "unknown" {
			continue
		}
		for _, h := range hints {
			if line >= h.StartLine && line <= h.EndLine {
				out[i] = Line{Agent: h.Tool, Type: h.Contributor, ModelID: textModel(h.Model)}
				n++
				break
			}
		}
	}
	return n, nil
}

// textModel bounds a model ID to the 250 characters Agent Trace allows.
func textModel(m string) string {
	if len(m) > 250 {
		return ""
	}
	return m
}

// MaxCommits bounds the commits one export walks.
const MaxCommits = 1000

func git(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.quotePath=false"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// exportable reports whether a path may appear in an export: built-in secret
// exclusions and .traceignore always apply.
func exportable(root, path string) bool {
	excluded, err := manifest.CaptureExcluded(root, path)
	return err == nil && !excluded
}

func toLines(origins []attribution.Origin) []Line {
	out := make([]Line, len(origins))
	for i, o := range origins {
		out[i] = Line{Agent: o.Agent, TurnID: o.TurnID, Step: o.Step}
	}
	return out
}

// Commits resolves a revision or an A..B range to commits, oldest first.
func Commits(root, rev string) ([]string, error) {
	if strings.HasPrefix(rev, "-") {
		return nil, fmt.Errorf("invalid revision %q", rev)
	}
	var out []byte
	var err error
	if strings.Contains(rev, "..") {
		out, err = git(root, "rev-list", "--reverse", "--no-merges", fmt.Sprintf("--max-count=%d", MaxCommits+1), rev, "--")
	} else {
		out, err = git(root, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	}
	if err != nil {
		return nil, err
	}
	shas := strings.Fields(string(out))
	if len(shas) > MaxCommits {
		return nil, fmt.Errorf("range has more than %d commits", MaxCommits)
	}
	return shas, nil
}

// addedLines lists the lines of newPath that sha added or changed against its
// first parent.
func addedLines(root, sha, oldPath, newPath string) ([]int, error) {
	args := []string{"diff-tree", "-p", "-U0", "-M", "--root", "--no-commit-id", "--no-color", "--no-ext-diff", sha, "--", newPath}
	if oldPath != newPath {
		args = append(args, oldPath)
	}
	out, err := git(root, args...)
	if err != nil {
		return nil, err
	}
	var lines []int
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		t := sc.Text()
		if !strings.HasPrefix(t, "@@ ") {
			continue
		}
		fields := strings.Fields(t)
		if len(fields) < 3 || !strings.HasPrefix(fields[2], "+") {
			continue
		}
		start, count := fields[2][1:], "1"
		if i := strings.IndexByte(start, ','); i >= 0 {
			start, count = start[:i], start[i+1:]
		}
		s, err1 := strconv.Atoi(start)
		n, err2 := strconv.Atoi(count)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("parsing diff hunk %q", t)
		}
		for i := 0; i < n; i++ {
			lines = append(lines, s+i)
		}
	}
	return lines, sc.Err()
}

func commitTime(root, sha string) (time.Time, error) {
	out, err := git(root, "show", "-s", "--format=%ct", sha)
	if err != nil {
		return time.Time{}, err
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	return time.Unix(sec, 0), err
}

// CommitRecord builds the record for the lines one commit introduced.
func CommitRecord(db attribution.Querier, blobs *blob.Store, root, sha string) (Record, error) {
	at, err := commitTime(root, sha)
	if err != nil {
		return Record{}, err
	}
	changes, err := gitx.ChangedPaths(root, sha)
	if err != nil {
		return Record{}, err
	}
	turns := map[string]map[string]any{}
	imported := 0
	var files []File
	for _, ch := range changes {
		if ch.Status == 'D' || !exportable(root, ch.Path) {
			continue
		}
		lines, err := addedLines(root, sha, ch.OldPath, ch.Path)
		if err != nil {
			return Record{}, err
		}
		if len(lines) == 0 {
			continue
		}
		origins, err := attribution.ExplainCommitLines(db, blobs, root, sha, ch.Path, lines)
		if err != nil {
			return Record{}, err
		}
		resolved := toLines(origins)
		if hinted, err := applyHints(db, sha, ch.Path, lines, resolved); err != nil {
			return Record{}, err
		} else if hinted > 0 {
			imported += hinted
		}
		var file File
		for i, start := 0, 0; i <= len(lines); i++ {
			if i == len(lines) || (i > start && lines[i] != lines[i-1]+1) {
				f := FileRecord(ch.Path, lines[start], resolved[start:i], turns)
				if file.Path == "" {
					file = f
				} else {
					MergeFile(&file, f)
				}
				start = i
			}
		}
		files = append(files, file)
	}
	m := metadata("commit", turns)
	if imported > 0 {
		m["imported_hint_lines"] = map[string]int{interop.SourceGitAI: imported}
	}
	return NewRecord(sha, at, files, m)
}

func metadata(scope string, turns map[string]map[string]any) map[string]any {
	m := map[string]any{"scope": scope}
	if len(turns) > 0 {
		m["turns"] = turns
	}
	return m
}

// WorkingTreeRecord builds one record for the working copy of every file
// with recorded edits. Line numbers refer to the working copy, so the record
// carries no vcs revision.
func WorkingTreeRecord(db attribution.Querier, blobs *blob.Store, root string, now time.Time) (Record, error) {
	rows, err := db.Query(`SELECT DISTINCT path FROM ops WHERE path!='' ORDER BY path`)
	if err != nil {
		return Record{}, fmt.Errorf("listing edited paths: %w", err)
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return Record{}, err
		}
		paths = append(paths, p)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return Record{}, err
	}
	turns := map[string]map[string]any{}
	var files []File
	for _, p := range paths {
		if !manifest.Portable(p) || !exportable(root, p) {
			continue
		}
		content, ok, err := readWorking(root, p)
		if err != nil {
			return Record{}, err
		}
		if !ok || content == "" {
			continue
		}
		n := strings.Count(strings.TrimSuffix(content, "\n"), "\n") + 1
		lines := make([]int, n)
		for i := range lines {
			lines[i] = i + 1
		}
		origins, err := attribution.ExplainLines(db, blobs, root, p, content, lines, false)
		if err != nil {
			return Record{}, err
		}
		flat := make([]attribution.Origin, len(origins))
		for i, o := range origins {
			flat[i] = o[len(o)-1]
		}
		files = append(files, FileRecord(p, 1, toLines(flat), turns))
	}
	m := metadata("working-tree", turns)
	if head, err := gitx.Head(root); err == nil {
		m["head"] = head
	}
	return NewRecord("", now, files, m)
}

// readWorking reads a regular, bounded working-copy file.
func readWorking(root, rel string) (string, bool, error) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || info.Size() > attribution.MaxTextBytes {
		return "", false, nil
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", false, err
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return "", false, nil
	}
	return string(b), true, nil
}
