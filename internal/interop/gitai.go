// Package interop reads other provenance tools' Git metadata, read-only.
package interop

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

// GitAINotesRef is the notes ref the Git AI Standard v3.0.0 requires
// (https://github.com/git-ai-project/git-ai/blob/main/specs/git_ai_standard_v3.0.0.md).
const GitAINotesRef = "refs/notes/ai"

// SourceGitAI names git-ai hints in imported_attributions.
const SourceGitAI = "git-ai"

const (
	maxNoteBytes = 4 << 20
	maxNotes     = 100000
	// MaxHintsPerNote and MaxHintsPerPath bound what one note can store;
	// ranges past either limit are dropped.
	MaxHintsPerNote = 20000
	MaxHintsPerPath = 2000
)

// Hint is one imported attributed range of a file as committed in Commit.
type Hint struct {
	Commit, Path         string
	StartLine, EndLine   int
	Contributor          string // "ai" or "human"
	Tool, Model, Session string
}

// Report summarizes an import.
type Report struct {
	Source   string   `json:"source"`
	Ref      string   `json:"ref"`
	Notes    int      `json:"notes"`
	Imported int      `json:"imported"`
	Existing int      `json:"already_imported"`
	Skipped  []string `json:"skipped"`
}

type agentID struct {
	Tool  string `json:"tool"`
	ID    string `json:"id"`
	Model string `json:"model"`
}

type gitAIMetadata struct {
	SchemaVersion string `json:"schema_version"`
	BaseCommitSHA string `json:"base_commit_sha"`
	Prompts       map[string]struct {
		AgentID *agentID `json:"agent_id"`
	} `json:"prompts"`
	Sessions map[string]struct {
		AgentID *agentID `json:"agent_id"`
	} `json:"sessions"`
	Humans map[string]struct {
		Author string `json:"author"`
	} `json:"humans"`
}

var (
	sessionKey = regexp.MustCompile(`^(s_[0-9a-f]{14})::t_[0-9a-f]{14}$`)
	humanKey   = regexp.MustCompile(`^h_[0-9a-f]{14}$`)
	legacyKey  = regexp.MustCompile(`^[0-9a-f]{16}$`)
	fullSHA    = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
)

// ParseGitAINote parses one authorship log attached to commit. Ranges whose
// key cannot be resolved in the metadata are skipped. Human authors' names
// and emails are not kept.
func ParseGitAINote(commit string, note []byte) ([]Hint, error) {
	text := strings.ReplaceAll(string(note), "\r\n", "\n")
	attest, meta, ok := strings.Cut(text, "\n---\n")
	if !ok {
		if rest, found := strings.CutPrefix(text, "---\n"); found {
			attest, meta, ok = "", rest, true
		}
	}
	if !ok {
		return nil, errors.New("missing --- separator")
	}
	var md gitAIMetadata
	if err := json.Unmarshal([]byte(meta), &md); err != nil {
		return nil, fmt.Errorf("parsing metadata: %w", err)
	}
	if !strings.HasPrefix(md.SchemaVersion, "authorship/3.") {
		return nil, fmt.Errorf("unsupported schema_version %q", md.SchemaVersion)
	}
	var hints []Hint
	perPath := map[string]int{}
	path := ""
	sc := bufio.NewScanner(strings.NewReader(attest))
	sc.Buffer(make([]byte, 64<<10), maxNoteBytes)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			path = line
			if strings.HasPrefix(line, `"`) {
				unq, err := strconv.Unquote(line)
				if err != nil {
					path = ""
					continue
				}
				path = unq
			}
			continue
		}
		entry, found := strings.CutPrefix(line, "  ")
		if !found || path == "" {
			continue
		}
		key, ranges, found := strings.Cut(entry, " ")
		if !found {
			continue
		}
		h := Hint{Commit: commit, Path: path}
		switch {
		case sessionKey.MatchString(key):
			s, ok := md.Sessions[sessionKey.FindStringSubmatch(key)[1]]
			if !ok || s.AgentID == nil {
				continue
			}
			h.Contributor, h.Tool, h.Model, h.Session = "ai", s.AgentID.Tool, s.AgentID.Model, sessionKey.FindStringSubmatch(key)[1]
		case humanKey.MatchString(key):
			if _, ok := md.Humans[key]; !ok {
				continue
			}
			h.Contributor, h.Session = "human", key
		case legacyKey.MatchString(key):
			p, ok := md.Prompts[key]
			if !ok || p.AgentID == nil {
				continue
			}
			h.Contributor, h.Tool, h.Model, h.Session = "ai", p.AgentID.Tool, p.AgentID.Model, key
		default:
			continue
		}
		for _, r := range strings.Split(ranges, ",") {
			a, b, isRange := strings.Cut(r, "-")
			start, err1 := strconv.Atoi(a)
			end := start
			var err2 error
			if isRange {
				end, err2 = strconv.Atoi(b)
			}
			if err1 != nil || err2 != nil || start < 1 || end < start {
				continue
			}
			if len(hints) >= MaxHintsPerNote {
				return hints, nil
			}
			if perPath[path] >= MaxHintsPerPath {
				break
			}
			perPath[path]++
			h.StartLine, h.EndLine = start, end
			hints = append(hints, h)
		}
	}
	return hints, sc.Err()
}

func readGit(root string, limit int64, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", root, "--no-pager"}, args...)...)
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(out, limit+1))
	_, _ = io.Copy(io.Discard, out)
	if err := errors.Join(readErr, cmd.Wait()); err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("git %s: output larger than %d bytes", args[0], limit)
	}
	return b, nil
}

// ImportGitAI reads every note under refs/notes/ai with read-only Git
// commands and stores its ranges as attribution hints. Notes, refs and
// commits are never written; importing again adds nothing new.
func ImportGitAI(db *sql.DB, root string) (Report, error) {
	report := Report{Source: SourceGitAI, Ref: GitAINotesRef, Skipped: []string{}}
	if _, err := readGit(root, 1<<10, "rev-parse", "--verify", "--quiet", GitAINotesRef); err != nil {
		return report, nil
	}
	list, err := readGit(root, 64<<20, "notes", "--ref="+GitAINotesRef, "list")
	if err != nil {
		return report, err
	}
	now := time.Now().UnixMilli()
	for _, line := range strings.Split(strings.TrimSpace(string(list)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !fullSHA.MatchString(fields[0]) || !fullSHA.MatchString(fields[1]) {
			continue
		}
		if report.Notes++; report.Notes > maxNotes {
			return report, fmt.Errorf("more than %d notes", maxNotes)
		}
		blob, commit := fields[0], fields[1]
		note, err := readGit(root, maxNoteBytes, "cat-file", "blob", blob)
		if err != nil {
			report.Skipped = append(report.Skipped, commit+": "+err.Error())
			continue
		}
		hints, err := ParseGitAINote(commit, note)
		if err != nil {
			report.Skipped = append(report.Skipped, commit+": "+err.Error())
			continue
		}
		imported, existing, err := storeHints(db, hints, now)
		if err != nil {
			return report, err
		}
		report.Imported += imported
		report.Existing += existing
	}
	return report, nil
}

func storeHints(db *sql.DB, hints []Hint, now int64) (int, int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	imported, existing := 0, 0
	for _, h := range hints {
		if manifest.Excluded(h.Path) || !manifest.Portable(h.Path) {
			continue
		}
		res, err := tx.Exec(`INSERT OR IGNORE INTO imported_attributions(source,commit_sha,path,start_line,end_line,
			contributor,tool,model,conversation,imported_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			SourceGitAI, h.Commit, h.Path, h.StartLine, h.EndLine, h.Contributor, h.Tool, h.Model, h.Session, now)
		if err != nil {
			return 0, 0, fmt.Errorf("storing hint: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			imported++
		} else {
			existing++
		}
	}
	return imported, existing, tx.Commit()
}

// HintsFor returns the imported hints for path as committed in commit.
func HintsFor(db interface {
	Query(string, ...any) (*sql.Rows, error)
}, commit, path string) ([]Hint, error) {
	rows, err := db.Query(`SELECT start_line,end_line,contributor,tool,model,conversation FROM imported_attributions
		WHERE commit_sha=? AND path=? ORDER BY start_line,end_line LIMIT ?`, commit, path, MaxHintsPerPath)
	if err != nil {
		return nil, fmt.Errorf("reading hints: %w", err)
	}
	var out []Hint
	for rows.Next() {
		h := Hint{Commit: commit, Path: path}
		if err := rows.Scan(&h.StartLine, &h.EndLine, &h.Contributor, &h.Tool, &h.Model, &h.Session); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, h)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}
