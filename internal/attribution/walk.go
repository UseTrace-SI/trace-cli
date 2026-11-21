package attribution

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
	"github.com/UseTrace-SI/trace-cli/internal/why"
)

// Confidence describes how a line was matched to its origin.
type Confidence string

const (
	// Exact: every step matched the line byte for byte.
	Exact Confidence = "exact"
	// Normalized: some step matched the line only after ignoring formatting.
	Normalized Confidence = "normalized"
	// Inferred: the line was matched to a commit by content because no
	// recorded rewrite named its source.
	Inferred Confidence = "inferred"
	// Uncertain: no origin could be established.
	Uncertain Confidence = "uncertain"
)

func weaker(a, b Confidence) Confidence {
	rank := map[Confidence]int{Exact: 0, Normalized: 1, Inferred: 2, Uncertain: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

const (
	messageLargeEdit = "origin uncertain after a large edit"
	messageUnmatched = "origin uncertain: line cannot be matched to the recorded history"
	messageBaseline  = "present at baseline"
	messageHuman     = "edited by you"
	messageReformat  = "reformatted"
)

// verdict is the origin of one line before turn details are looked up.
type verdict struct {
	kind       string // "turn", "human", "baseline" or "unknown"
	turnID     string
	agent      string
	at         int64
	symbol     string
	message    string
	confidence Confidence
	// commit is the commit whose content was explained, origin the commit in
	// which the line was first committed; rewrites lead from origin to commit.
	commit, origin string
	rewrites       []Rewrite
	// trail lists formatting-only edits passed on the way to the origin.
	trail []verdict
}

type change struct {
	id, before, after, turnID, detail string
	at                                int64
	start, end                        int
}

type cachedText struct {
	text      string
	truncated bool
}

// walker reads recorded history. It caches file states and per-path change
// lists for the duration of one command.
type walker struct {
	db      Querier
	blobs   *blob.Store
	texts   map[string]cachedText
	changes map[string][]change
	base    *baselineState
	turns   map[string]turnDetails
}

type baselineState struct {
	loaded bool
	exists bool
	at     int64
	files  manifest.Manifest
}

func newWalker(db Querier, blobs *blob.Store) *walker {
	return &walker{db: db, blobs: blobs, texts: map[string]cachedText{"": {}}, changes: map[string][]change{}}
}

func (w *walker) text(hash string) (cachedText, error) {
	if value, ok := w.texts[hash]; ok {
		return value, nil
	}
	text, truncated, err := textLimited(w.blobs, blob.Hash(hash))
	if err != nil {
		return cachedText{}, err
	}
	value := cachedText{text: text, truncated: truncated}
	if len(w.texts) >= 256 {
		w.texts = map[string]cachedText{"": {}}
	}
	w.texts[hash] = value
	return value, nil
}

func (w *walker) fileChanges(file string) ([]change, error) {
	if changes, ok := w.changes[file]; ok {
		return changes, nil
	}
	rows, err := w.db.Query(`SELECT id,before_hash,after_hash,COALESCE(turn_id,''),at,COALESCE(detail,''),COALESCE(start_line,0),COALESCE(end_line,0) FROM ops
		WHERE path=? AND (before_hash IS NOT NULL OR after_hash IS NOT NULL) ORDER BY at DESC,id DESC`, file)
	if err != nil {
		return nil, fmt.Errorf("querying file history: %w", err)
	}
	var changes []change
	for rows.Next() {
		var ch change
		var before, after sql.NullString
		if err := rows.Scan(&ch.id, &before, &after, &ch.turnID, &ch.at, &ch.detail, &ch.start, &ch.end); err != nil {
			rows.Close()
			return nil, err
		}
		ch.before, ch.after = before.String, after.String
		changes = append(changes, ch)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	w.changes[file] = changes
	return changes, nil
}

func (w *walker) baseline() (*baselineState, error) {
	if w.base != nil {
		return w.base, nil
	}
	w.base = &baselineState{loaded: true}
	var hash string
	err := w.db.QueryRow("SELECT manifest_hash,created_at FROM keyframes WHERE seq=0").Scan(&hash, &w.base.at)
	if errors.Is(err, sql.ErrNoRows) {
		return w.base, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding baseline: %w", err)
	}
	files, err := manifest.Load(blob.Hash(hash), w.blobs)
	if err != nil {
		return nil, fmt.Errorf("loading baseline: %w", err)
	}
	w.base.exists, w.base.files = true, files
	return w.base, nil
}

func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// walk explains 1-based lines of text, the content of file at some moment,
// through the recorded changes made at or before cutoff (0 means no cutoff).
//
// Recorded states form a timeline. A state that recurs later, such as a file
// re-created by a rebase, checkout or rewind, is explained from its earliest
// occurrence: the changes in between returned the file to that exact content,
// so they cannot have authored any of its lines.
func (w *walker) walk(file, text string, lines []int, cutoff int64, all bool) ([]verdict, error) {
	recorded, err := w.fileChanges(file)
	if err != nil {
		return nil, err
	}
	changes := recorded
	if cutoff > 0 {
		changes = nil
		for _, ch := range recorded {
			if ch.at <= cutoff {
				changes = append(changes, ch)
			}
		}
	}
	oldest := map[string]int{}
	for i, ch := range changes {
		oldest[ch.before] = i
		oldest[ch.after] = i
	}
	type pending struct {
		line       int
		confidence Confidence
		trail      []verdict
	}
	out := make([]verdict, len(lines))
	state := make([]*pending, len(lines))
	open := 0
	count := len(splitLines(text))
	for k, line := range lines {
		if line < 1 || line > count {
			out[k] = verdict{kind: "unknown", message: messageUnmatched, confidence: Uncertain}
			continue
		}
		state[k] = &pending{line: line, confidence: Exact}
		open++
	}
	finish := func(k int, v verdict) {
		v.trail = state[k].trail
		out[k] = v
		state[k] = nil
		open--
	}
	current, currentHash := text, contentHash(text)
	for i := 0; i < len(changes) && open > 0; {
		if at, ok := oldest[currentHash]; ok && at >= i {
			if changes[at].after == currentHash {
				i = at
			} else {
				i = at + 1
				continue
			}
		}
		ch := changes[i]
		j := i + 1
		for j < len(changes) && changes[j].before == ch.before && changes[j].after == ch.after && changes[j].turnID == ch.turnID {
			j++
		}
		group := changes[i:j]
		i = j
		oldText, err := w.text(ch.before)
		if err != nil {
			return nil, err
		}
		newText, err := w.text(ch.after)
		if err != nil {
			return nil, err
		}
		if oldText.truncated || newText.truncated {
			for k := range state {
				if state[k] != nil {
					finish(k, verdict{kind: "unknown", message: messageLargeEdit, confidence: Uncertain})
				}
			}
			break
		}
		if currentHash != ch.after {
			aligned := why.MapLines(newText.text, current)
			for k, p := range state {
				if p == nil {
					continue
				}
				match := aligned[p.line-1]
				switch {
				case match.Line < 0:
					finish(k, verdict{kind: "unknown", message: messageLargeEdit, confidence: Uncertain})
				case match.Line == 0:
					finish(k, verdict{kind: "unknown", message: messageUnmatched, confidence: Uncertain})
				default:
					p.line = match.Line
					if match.Normalized {
						p.confidence = weaker(p.confidence, Normalized)
					}
				}
			}
		}
		current, currentHash = oldText.text, ch.before
		var mapped []why.Match
		for k, p := range state {
			if p == nil {
				continue
			}
			laterInFile := true
			for _, candidate := range group {
				if candidate.start == 0 || candidate.start <= p.line {
					laterInFile = false
					break
				}
			}
			if laterInFile {
				continue
			}
			if mapped == nil {
				mapped = why.MapLines(oldText.text, newText.text)
			}
			match := mapped[p.line-1]
			switch {
			case match.Line < 0:
				finish(k, verdict{kind: "unknown", message: messageLargeEdit, confidence: Uncertain})
			case match.Line == 0:
				finish(k, editVerdict(group, p.line, p.confidence))
			default:
				if match.Normalized {
					if all {
						edit := editVerdict(group, p.line, p.confidence)
						edit.message = messageReformat
						p.trail = append(p.trail, edit)
					}
					p.confidence = weaker(p.confidence, Normalized)
				}
				p.line = match.Line
			}
		}
	}
	if open == 0 {
		return out, nil
	}
	if len(changes) == 0 {
		// Nothing was recorded for this file: only the baseline snapshot can
		// show that a line predates recording.
		base, err := w.baseline()
		if err != nil {
			return nil, err
		}
		if base.exists {
			entry, ok := base.files[file]
			var aligned []why.Match
			if ok {
				baseText, err := w.text(string(entry.Hash))
				if err != nil {
					return nil, err
				}
				if !baseText.truncated {
					aligned = why.MapLines(baseText.text, current)
				}
			}
			for k, p := range state {
				if p == nil {
					continue
				}
				if aligned == nil || aligned[p.line-1].Line <= 0 {
					finish(k, verdict{kind: "unknown", message: messageUnmatched, confidence: Uncertain})
					continue
				}
				confidence := p.confidence
				if aligned[p.line-1].Normalized {
					confidence = weaker(confidence, Normalized)
				}
				finish(k, verdict{kind: "baseline", message: messageBaseline, confidence: confidence})
			}
			return out, nil
		}
	}
	for k, p := range state {
		if p != nil {
			finish(k, verdict{kind: "baseline", message: messageBaseline, confidence: p.confidence})
		}
	}
	return out, nil
}

func editVerdict(group []change, line int, confidence Confidence) verdict {
	ch := group[0]
	v := verdict{at: ch.at, confidence: confidence}
	for _, candidate := range group {
		if candidate.detail != "" && line >= candidate.start && line <= candidate.end {
			v.symbol = candidate.detail
			break
		}
	}
	if ch.turnID == "" {
		v.kind, v.message = "human", messageHuman
	} else {
		v.kind, v.turnID = "turn", ch.turnID
	}
	return v
}
