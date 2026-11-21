package attribution

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/gitx"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/why"
)

// Commit attribution. When the post-commit hook reports a commit, Trace
// records who wrote each line the commit introduced and which commits it
// rewrote (amend, rebase, cherry-pick or squash). Those facts are stored in
// the commit row, which travels with exported and synced history, so a later
// `trace why` in any checkout of the repository can follow `git blame` to the
// commit and from there back through rewrites to the recorded edit.

const (
	recordVersion   = 1
	maxRecordBytes  = 256 << 10
	maxRecordPaths  = 200
	maxResolveDepth = 24
	maxCandidates   = 8
)

// Rewrite kinds stored in commit rows and reported in rewrite chains.
const (
	KindAmend       = "amend"
	KindRebase      = "rebase"
	KindSquash      = "squash"
	KindCherryPick  = "cherry-pick"
	KindMergeSquash = "merge-squash"
	KindPatchID     = "patch-id"
	KindInferred    = "inferred"
)

type commitRecord struct {
	Version int                 `json:"v"`
	Files   map[string][][3]int `json:"files"`
	Origins []recordOrigin      `json:"origins"`
	Partial bool                `json:"partial,omitempty"`
}

type recordOrigin struct {
	Kind       string     `json:"kind"`
	Turn       string     `json:"turn,omitempty"`
	Agent      string     `json:"agent,omitempty"`
	At         int64      `json:"at,omitempty"`
	Symbol     string     `json:"symbol,omitempty"`
	Confidence Confidence `json:"confidence"`
	Origin     string     `json:"origin,omitempty"`
	Rewrites   []Rewrite  `json:"rewrites,omitempty"`
}

type source struct{ sha, kind string }

type commitInfo struct {
	sha      string
	observed bool
	at       int64
	patchID  string
	kind     string
	sources  []source
	record   *commitRecord
	// trusted is false when local post-rewrite pairs name sources the stored
	// record did not use; the record is then recomputed.
	trusted bool
	parents []string
}

type fileContent struct {
	text string
	ok   bool
}

type blameLines struct {
	lines []gitx.BlameLine
	ok    bool
}

// resolver explains lines of committed files. It caches Git reads for the
// duration of one command.
type resolver struct {
	w        *walker
	root     string
	all      bool
	infos    map[string]*commitInfo
	contents map[string]fileContent
	blames   map[string]blameLines
	cands    map[string][]string
}

func newResolver(db Querier, blobs *blob.Store, root string, all bool) *resolver {
	return &resolver{w: newWalker(db, blobs), root: root, all: all, infos: map[string]*commitInfo{},
		contents: map[string]fileContent{}, blames: map[string]blameLines{}, cands: map[string][]string{}}
}

func (r *resolver) content(sha, path string) (string, bool) {
	key := sha + "\x00" + path
	if c, ok := r.contents[key]; ok {
		return c.text, c.ok
	}
	b, truncated, err := gitx.ShowFile(r.root, sha, path, MaxTextBytes)
	c := fileContent{text: string(b), ok: err == nil && !truncated && bytes.IndexByte(b, 0) < 0}
	r.contents[key] = c
	return c.text, c.ok
}

func (r *resolver) blame(sha, path string) ([]gitx.BlameLine, bool) {
	key := sha + "\x00" + path
	if b, ok := r.blames[key]; ok {
		return b.lines, b.ok
	}
	lines, err := gitx.Blame(r.root, sha, path)
	b := blameLines{lines: lines, ok: err == nil}
	r.blames[key] = b
	return b.lines, b.ok
}

func (r *resolver) info(sha string) (*commitInfo, error) {
	if info, ok := r.infos[sha]; ok {
		return info, nil
	}
	info := &commitInfo{sha: sha, trusted: true}
	var at sql.NullInt64
	var patchID, kind, from, record sql.NullString
	err := r.w.db.QueryRow("SELECT at,patch_id,rewrite_kind,rewritten_from,attribution FROM commits WHERE sha=?", sha).
		Scan(&at, &patchID, &kind, &from, &record)
	switch {
	case err == nil:
		info.observed, info.at, info.patchID, info.kind = true, at.Int64, patchID.String, kind.String
		for _, s := range strings.Fields(from.String) {
			if gitx.ValidSHA(s) && s != sha {
				info.sources = append(info.sources, source{s, info.kind})
			}
		}
		info.record = decodeRecord(record.String)
	case errors.Is(err, sql.ErrNoRows):
	default:
		return nil, fmt.Errorf("reading commit: %w", err)
	}
	rows, err := r.w.db.Query("SELECT old_sha,kind FROM commit_rewrites WHERE new_sha=? ORDER BY at,id", sha)
	if err != nil {
		return nil, fmt.Errorf("reading rewrites: %w", err)
	}
	var local []source
	for rows.Next() {
		var s source
		if err := rows.Scan(&s.sha, &s.kind); err != nil {
			rows.Close()
			return nil, err
		}
		local = append(local, s)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, s := range local {
		if !gitx.ValidSHA(s.sha) || s.sha == sha || hasSource(info.sources, s.sha) {
			continue
		}
		info.sources = append(info.sources, s)
		info.trusted = false
	}
	if c, err := gitx.ReadCommit(r.root, sha); err == nil {
		info.parents = c.Parents
		if !info.observed {
			// Unobserved commits are explained by edits recorded before Git
			// says they were committed.
			info.at = c.Committed*1000 + 999
		}
	}
	r.infos[sha] = info
	return info, nil
}

func hasSource(sources []source, sha string) bool {
	for _, s := range sources {
		if s.sha == sha {
			return true
		}
	}
	return false
}

func decodeRecord(raw string) *commitRecord {
	if raw == "" || len(raw) > maxRecordBytes {
		return nil
	}
	var record commitRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil || record.Version != recordVersion {
		return nil
	}
	for path, runs := range record.Files {
		if !manifest.Portable(path) {
			return nil
		}
		for _, run := range runs {
			if run[0] < 1 || run[1] < run[0] || run[2] < 0 || run[2] >= len(record.Origins) {
				return nil
			}
		}
	}
	for _, origin := range record.Origins {
		switch origin.Kind {
		case "turn", "human", "baseline", "unknown":
		default:
			return nil
		}
		switch origin.Confidence {
		case Exact, Normalized, Inferred, Uncertain:
		default:
			return nil
		}
	}
	return &record
}

func (record *commitRecord) lookup(path string, line int) (recordOrigin, bool) {
	for _, run := range record.Files[path] {
		if line >= run[0] && line <= run[1] {
			return record.Origins[run[2]], true
		}
	}
	return recordOrigin{}, false
}

func fromRecord(origin recordOrigin) verdict {
	v := verdict{kind: origin.Kind, turnID: origin.Turn, agent: origin.Agent, at: origin.At, symbol: origin.Symbol,
		confidence: origin.Confidence, origin: origin.Origin, rewrites: origin.Rewrites}
	switch v.kind {
	case "human":
		v.message = messageHuman
	case "baseline":
		v.message = messageBaseline
	case "unknown":
		v.message = messageUnmatched
	}
	return v
}

func unknownVerdict(message string) verdict {
	return verdict{kind: "unknown", message: message, confidence: Uncertain}
}

// resolveLines explains 1-based lines of path as committed in sha.
func (r *resolver) resolveLines(sha, path string, lines []int, depth int) ([]verdict, error) {
	out := make([]verdict, len(lines))
	if depth > maxResolveDepth || !gitx.ValidSHA(sha) {
		for k := range out {
			out[k] = unknownVerdict(messageUnmatched)
		}
		return out, nil
	}
	info, err := r.info(sha)
	if err != nil {
		return nil, err
	}
	var todo []int
	for k, line := range lines {
		if !r.all && info.trusted && info.record != nil {
			if origin, ok := info.record.lookup(path, line); ok {
				out[k] = fromRecord(origin)
				continue
			}
		}
		todo = append(todo, k)
	}
	if len(todo) == 0 {
		return out, nil
	}
	// Lines the record does not cover were last changed by an earlier commit,
	// or the record is missing: follow git blame inside sha.
	blamed, ok := r.blame(sha, path)
	type located struct {
		k    int
		line int
	}
	earlier := map[string][]located{}
	paths := map[string]string{}
	var own []int
	for _, k := range todo {
		line := lines[k]
		if !ok || line > len(blamed) || blamed[line-1].Commit == "" || blamed[line-1].Commit == sha {
			own = append(own, k)
			continue
		}
		b := blamed[line-1]
		earlier[b.Commit] = append(earlier[b.Commit], located{k, b.Line})
		paths[b.Commit] = b.Path
	}
	for commit, group := range earlier {
		at := make([]int, len(group))
		for i, g := range group {
			at[i] = g.line
		}
		verdicts, err := r.resolveLines(commit, paths[commit], at, depth+1)
		if err != nil {
			return nil, err
		}
		for i, g := range group {
			out[g.k] = verdicts[i]
		}
	}
	if len(own) > 0 {
		at := make([]int, len(own))
		for i, k := range own {
			at[i] = lines[k]
		}
		verdicts, err := r.computeLines(info, path, at, depth)
		if err != nil {
			return nil, err
		}
		for i, k := range own {
			out[k] = verdicts[i]
		}
	}
	return out, nil
}

// computeLines explains lines that sha itself introduced: through the commits
// it rewrote, then through recorded edits, and last through observed commits
// that contained the same lines.
func (r *resolver) computeLines(info *commitInfo, path string, lines []int, depth int) ([]verdict, error) {
	out := make([]verdict, len(lines))
	content, ok := r.content(info.sha, path)
	if !ok {
		for k := range out {
			out[k] = unknownVerdict(messageLargeEdit)
		}
		return out, nil
	}
	pending := make([]int, len(lines))
	for k := range pending {
		pending[k] = k
	}
	sources, err := r.sources(info)
	if err != nil {
		return nil, err
	}
	for _, src := range sources {
		if len(pending) == 0 {
			break
		}
		pending, err = r.transfer(info.sha, path, content, lines, pending, out, src, depth, false)
		if err != nil {
			return nil, err
		}
	}
	if len(pending) > 0 {
		at := make([]int, len(pending))
		for i, k := range pending {
			at[i] = lines[k]
		}
		var verdicts []verdict
		base, err := r.w.baseline()
		if err != nil {
			return nil, err
		}
		if base.exists && info.at > 0 && info.at < base.at {
			// Committed before Trace started recording this repository.
			verdicts = make([]verdict, len(at))
			for i := range verdicts {
				verdicts[i] = verdict{kind: "baseline", message: messageBaseline, confidence: Exact}
			}
		} else {
			verdicts, err = r.w.walk(path, content, at, info.at, r.all)
			if err != nil {
				return nil, err
			}
		}
		var weak []int
		for i, k := range pending {
			out[k] = verdicts[i]
			if verdicts[i].kind == "human" || verdicts[i].kind == "unknown" {
				weak = append(weak, k)
			}
		}
		if len(weak) > 0 && len(sources) == 0 {
			candidates, err := r.candidates(info, path)
			if err != nil {
				return nil, err
			}
			for _, candidate := range candidates {
				if len(weak) == 0 {
					break
				}
				weak, err = r.transfer(info.sha, path, content, lines, weak, out, source{candidate, KindInferred}, depth, true)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	for k := range out {
		if out[k].origin == "" && out[k].kind != "unknown" {
			out[k].origin = info.sha
		}
	}
	return out, nil
}

// transfer maps pending lines of content (path in sha) into src's version
// and explains them there. Lines it explains are written to out; the rest are
// returned. Inferred transfers accept only distinctive exact matches and only
// replace answers the recorded edits could not settle.
func (r *resolver) transfer(sha, path, content string, lines, pending []int, out []verdict, src source, depth int, inferred bool) ([]int, error) {
	srcContent, ok := r.content(src.sha, path)
	if !ok {
		return pending, nil
	}
	matches := why.MapLines(srcContent, content)
	var accepted []bool
	if inferred {
		accepted = distinctiveMatches(srcContent, content, matches)
	}
	var mapped, at []int
	var rest []int
	for _, k := range pending {
		m := matches[lines[k]-1]
		if m.Line <= 0 || (inferred && (m.Normalized || !accepted[lines[k]-1])) {
			rest = append(rest, k)
			continue
		}
		mapped = append(mapped, k)
		at = append(at, m.Line)
	}
	if len(mapped) == 0 {
		return pending, nil
	}
	verdicts, err := r.resolveLines(src.sha, path, at, depth+1)
	if err != nil {
		return nil, err
	}
	for i, k := range mapped {
		v := verdicts[i]
		if v.kind == "unknown" || (inferred && v.kind == "human" && out[k].kind == "human") {
			rest = append(rest, k)
			continue
		}
		if matches[lines[k]-1].Normalized {
			v.confidence = weaker(v.confidence, Normalized)
		}
		if inferred {
			v.confidence = weaker(v.confidence, Inferred)
		}
		if v.origin == "" {
			v.origin = src.sha
		}
		v.rewrites = append(append([]Rewrite(nil), v.rewrites...), Rewrite{From: src.sha, To: sha, Kind: src.kind})
		v.trail = nil
		out[k] = v
	}
	sort.Ints(rest)
	return rest, nil
}

// distinctiveMatches marks exact matches that are part of a run of at least
// three matched lines, or that are long lines unique in both texts. Short,
// repeated lines such as "}" are never matched by content alone.
func distinctiveMatches(oldText, newText string, matches []why.Match) []bool {
	newLines := splitLines(newText)
	counts := map[string]int{}
	for _, line := range splitLines(oldText) {
		counts[line]++
	}
	for _, line := range newLines {
		counts[line] += 1 << 16
	}
	accepted := make([]bool, len(matches))
	for j := 0; j < len(matches); {
		if matches[j].Line <= 0 || matches[j].Normalized {
			j++
			continue
		}
		start := j
		for j+1 < len(matches) && matches[j+1].Line == matches[j].Line+1 && !matches[j+1].Normalized {
			j++
		}
		run := j - start + 1
		for i := start; i <= j; i++ {
			line := strings.TrimSpace(newLines[i])
			accepted[i] = run >= 3 || (len(line) >= 12 && counts[newLines[i]] == 1+1<<16)
		}
		j++
	}
	return accepted
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// sources lists the commits sha rewrote: those named when it was observed,
// local post-rewrite pairs, and otherwise an earlier observed commit with the
// same patch ID.
func (r *resolver) sources(info *commitInfo) ([]source, error) {
	var out []source
	for _, s := range info.sources {
		if gitx.CommitExists(r.root, s.sha) || r.knownCommit(s.sha) {
			out = append(out, s)
		}
	}
	if len(out) > 0 || len(info.parents) > 1 {
		return out, nil
	}
	patchID := info.patchID
	if patchID == "" && !info.observed {
		patchID, _ = gitx.PatchID(r.root, info.sha)
	}
	if patchID == "" {
		return nil, nil
	}
	rows, err := r.w.db.Query("SELECT sha FROM commits WHERE patch_id=? AND sha<>? AND at<? ORDER BY at LIMIT 4", patchID, info.sha, max(info.at, 1))
	if err != nil {
		return nil, fmt.Errorf("finding identical patches: %w", err)
	}
	var shas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, err
		}
		shas = append(shas, s)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, s := range shas {
		out = append(out, source{s, KindPatchID})
	}
	return out, nil
}

func (r *resolver) knownCommit(sha string) bool {
	var n int
	return r.w.db.QueryRow("SELECT COUNT(*) FROM commits WHERE sha=?", sha).Scan(&n) == nil && n > 0
}

// candidates lists recent observed commits, other than ancestors of sha, whose
// records show they introduced lines in path.
func (r *resolver) candidates(info *commitInfo, path string) ([]string, error) {
	key := info.sha + "\x00" + path
	if c, ok := r.cands[key]; ok {
		return c, nil
	}
	rows, err := r.w.db.Query("SELECT sha,COALESCE(attribution,'') FROM commits WHERE sha<>? AND at<=? ORDER BY at DESC LIMIT 40", info.sha, info.at)
	if err != nil {
		return nil, fmt.Errorf("finding candidate commits: %w", err)
	}
	type row struct{ sha, record string }
	var found []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.sha, &x.record); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, x)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	var out []string
	for _, x := range found {
		record := decodeRecord(x.record)
		if record == nil || len(record.Files[path]) == 0 || gitx.IsAncestor(r.root, x.sha, info.sha) {
			continue
		}
		out = append(out, x.sha)
		if len(out) == maxCandidates {
			break
		}
	}
	r.cands[key] = out
	return out, nil
}

// CommitEvent is what the Git hooks reported about a new commit.
type CommitEvent struct {
	SHA      string
	Previous string // HEAD before the commit, from the reflog
	Action   string // reflog action, such as "commit (amend)"
	Sources  []string
	At       int64
	// RewriteKind and Rewritten are set when the commit is first seen in a
	// post-rewrite report instead of by the post-commit hook.
	RewriteKind string
	Rewritten   []string
}

// CommitDescription holds the columns stored for an observed commit.
type CommitDescription struct {
	PatchID       string
	Kind          string
	RewrittenFrom string
	Attribution   string
}

// DescribeCommit classifies a newly observed commit and records who wrote the
// lines it introduced. It only reads the repository.
func DescribeCommit(db Querier, blobs *blob.Store, root string, ev CommitEvent) (CommitDescription, error) {
	var d CommitDescription
	commit, err := gitx.ReadCommit(root, ev.SHA)
	if err != nil {
		return d, err
	}
	if len(commit.Parents) <= 1 {
		if d.PatchID, err = gitx.PatchID(root, ev.SHA); err != nil {
			d.PatchID = ""
		}
	}
	kind, named := classify(ev, commit)
	var sources []source
	for _, sha := range named {
		if gitx.ValidSHA(sha) && sha != ev.SHA && !hasSource(sources, sha) && gitx.CommitExists(root, sha) {
			sources = append(sources, source{sha, kind})
		}
	}
	r := newResolver(db, blobs, root, false)
	info := &commitInfo{sha: ev.SHA, observed: true, at: ev.At, patchID: d.PatchID, kind: kind, sources: sources,
		trusted: true, parents: commit.Parents}
	if len(sources) == 0 {
		// No hook named a source; an identical patch observed earlier is the
		// commit this one copies (a cherry-pick or a rebase done elsewhere).
		if found, err := r.sources(info); err == nil && len(found) > 0 {
			info.sources, info.kind = found[:1], KindPatchID
		}
	}
	r.infos[ev.SHA] = info
	d.Kind = info.kind
	var shas []string
	for _, s := range info.sources {
		shas = append(shas, s.sha)
	}
	d.RewrittenFrom = strings.Join(shas, " ")
	if len(commit.Parents) > 1 {
		return d, nil
	}
	record, err := r.recordCommit(info, commit)
	if err != nil {
		return d, err
	}
	d.Attribution = record
	return d, nil
}

// classify names the commits a new commit rewrote, from what Git recorded
// while making it.
func classify(ev CommitEvent, commit gitx.Commit) (string, []string) {
	if ev.RewriteKind != "" {
		return ev.RewriteKind, ev.Rewritten
	}
	switch ev.Action {
	case "commit (amend)":
		return KindAmend, []string{ev.Previous}
	case "rebase (squash)", "rebase (fixup)", "rebase -i (squash)", "rebase -i (fixup)":
		return KindSquash, append([]string{ev.Previous}, ev.Sources...)
	case "rebase (pick)", "rebase (reword)", "rebase (edit)", "rebase -i (pick)", "rebase -i (reword)", "rebase -i (edit)":
		return KindRebase, ev.Sources
	case "cherry-pick":
		return KindCherryPick, ev.Sources
	}
	// `git merge --squash` leaves the squashed commits in the default message.
	if strings.HasPrefix(commit.Message, "Squashed commit of the following:") {
		var named []string
		for _, line := range strings.Split(commit.Message, "\n") {
			if sha, ok := strings.CutPrefix(line, "commit "); ok && gitx.ValidSHA(strings.TrimSpace(sha)) {
				named = append(named, strings.TrimSpace(sha))
			}
		}
		return KindMergeSquash, named
	}
	return "", nil
}

// recordCommit explains the lines a non-merge commit introduced in each text
// file it changed and encodes them as runs.
func (r *resolver) recordCommit(info *commitInfo, commit gitx.Commit) (string, error) {
	changes, err := gitx.ChangedPaths(r.root, info.sha)
	if err != nil {
		return "", nil
	}
	record := commitRecord{Version: recordVersion, Files: map[string][][3]int{}}
	index := map[string]int{}
	var paths []string
	for _, change := range changes {
		if change.Status == 'D' || !manifest.Portable(change.Path) {
			continue
		}
		if len(record.Files) >= maxRecordPaths {
			record.Partial = true
			break
		}
		content, ok := r.content(info.sha, change.Path)
		if !ok {
			continue
		}
		previous := ""
		if len(commit.Parents) == 1 && change.Status != 'A' {
			previous, _ = r.content(commit.Parents[0], change.OldPath)
		}
		var introduced []int
		for i, m := range why.MapLines(previous, content) {
			if m.Line <= 0 || m.Normalized {
				introduced = append(introduced, i+1)
			}
		}
		if len(introduced) == 0 {
			continue
		}
		verdicts, err := r.computeLines(info, change.Path, introduced, 0)
		if err != nil {
			return "", err
		}
		var runs [][3]int
		for i, v := range verdicts {
			origin := recordOrigin{Kind: v.kind, Turn: v.turnID, Agent: v.agent, At: v.at, Symbol: v.symbol,
				Confidence: v.confidence, Origin: v.origin, Rewrites: v.rewrites}
			if origin.Kind == "turn" && origin.Agent == "" {
				origin.Agent = r.turnAgent(origin.Turn)
			}
			key, err := json.Marshal(origin)
			if err != nil {
				return "", err
			}
			at, ok := index[string(key)]
			if !ok {
				at = len(record.Origins)
				index[string(key)] = at
				record.Origins = append(record.Origins, origin)
			}
			line := introduced[i]
			if n := len(runs); n > 0 && runs[n-1][2] == at && runs[n-1][1] == line-1 {
				runs[n-1][1] = line
			} else {
				runs = append(runs, [3]int{line, line, at})
			}
		}
		record.Files[change.Path] = runs
		paths = append(paths, change.Path)
	}
	for {
		b, err := json.Marshal(record)
		if err != nil {
			return "", err
		}
		if len(b) <= maxRecordBytes || len(paths) == 0 {
			return string(b), nil
		}
		// Keep the row within sync limits; later lookups recompute the rest.
		delete(record.Files, paths[len(paths)-1])
		paths = paths[:len(paths)-1]
		record.Partial = true
	}
}

func (r *resolver) turnAgent(turnID string) string {
	var agent string
	_ = r.w.db.QueryRow("SELECT s.agent FROM turns t JOIN sessions s ON s.id=t.session_id WHERE t.id=?", turnID).Scan(&agent)
	return agent
}

// ExplainLines explains lines of the working copy of file in a Git
// repository. Committed lines are followed through `git blame` to the commit
// that introduced them and from there through recorded rewrites; uncommitted
// lines are explained by the recorded edits alone. With all, each line also
// lists the formatting-only edits passed on the way to its origin.
func ExplainLines(db Querier, blobs *blob.Store, root, file, current string, lines []int, all bool) ([][]Origin, error) {
	verdicts, r, err := explainLines(db, blobs, root, file, current, lines, all)
	if err != nil {
		return nil, err
	}
	out := make([][]Origin, len(verdicts))
	for i, v := range verdicts {
		if out[i], err = r.w.origins(v, all); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func explainLines(db Querier, blobs *blob.Store, root, file, current string, lines []int, all bool) ([]verdict, *resolver, error) {
	r := newResolver(db, blobs, root, all)
	out := make([]verdict, len(lines))
	blamed, err := gitx.Blame(root, "", file)
	if err != nil || len(blamed) != len(splitLines(current)) {
		verdicts, err := r.w.walk(file, current, lines, 0, all)
		return verdicts, r, err
	}
	type located struct {
		k, line int
	}
	committed := map[string][]located{}
	paths := map[string]string{}
	var working []int
	for k, line := range lines {
		if line < 1 || line > len(blamed) || blamed[line-1].Commit == "" {
			working = append(working, k)
			continue
		}
		b := blamed[line-1]
		committed[b.Commit] = append(committed[b.Commit], located{k, b.Line})
		paths[b.Commit] = b.Path
	}
	if len(working) > 0 {
		at := make([]int, len(working))
		for i, k := range working {
			at[i] = lines[k]
		}
		verdicts, err := r.w.walk(file, current, at, 0, all)
		if err != nil {
			return nil, nil, err
		}
		for i, k := range working {
			out[k] = verdicts[i]
		}
	}
	commits := make([]string, 0, len(committed))
	for commit := range committed {
		commits = append(commits, commit)
	}
	sort.Strings(commits)
	for _, commit := range commits {
		group := committed[commit]
		at := make([]int, len(group))
		for i, g := range group {
			at[i] = g.line
		}
		verdicts, err := r.resolveLines(commit, paths[commit], at, 0)
		if err != nil {
			return nil, nil, err
		}
		for i, g := range group {
			v := verdicts[i]
			v.commit = commit
			if v.origin == "" && v.kind != "unknown" {
				v.origin = commit
			}
			out[g.k] = v
		}
	}
	return out, r, nil
}
