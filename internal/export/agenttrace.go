// Package export converts Trace history into interchange formats.
package export

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// SpecVersion is the Agent Trace specification version written in records.
// Agent Trace 0.1.0 (https://agent-trace.dev) constrains `version` to the
// pattern ^[0-9]+\.[0-9]+$, so the two-part form is used.
const SpecVersion = "0.1"

// MediaType is the media type Agent Trace defines for one record.
const MediaType = "application/vnd.agent-trace.record+json"

// Contributor is an Agent Trace contributor.
type Contributor struct {
	Type    string `json:"type"`
	ModelID string `json:"model_id,omitempty"`
}

// Range is an inclusive, 1-based line range.
type Range struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

// Conversation groups the ranges one conversation produced.
type Conversation struct {
	URL         string       `json:"url,omitempty"`
	Contributor *Contributor `json:"contributor,omitempty"`
	Ranges      []Range      `json:"ranges"`
}

// File lists the attributed ranges of one repository-relative path.
type File struct {
	Path          string         `json:"path"`
	Conversations []Conversation `json:"conversations"`
}

// VCS names the revision the line numbers refer to.
type VCS struct {
	Type     string `json:"type"`
	Revision string `json:"revision"`
}

// Tool names the producer of a record.
type Tool struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// Record is one Agent Trace record.
type Record struct {
	Version   string         `json:"version"`
	ID        string         `json:"id"`
	Timestamp string         `json:"timestamp"`
	VCS       *VCS           `json:"vcs,omitempty"`
	Tool      *Tool          `json:"tool,omitempty"`
	Files     []File         `json:"files"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// Line is the attributed origin of one line.
type Line struct {
	// Agent is the recording agent, or "human", "baseline" or "unknown".
	Agent  string
	TurnID string
	Step   int64
	// Type, when set, overrides the contributor type derived from Agent; it
	// marks a line explained only by an imported hint. ModelID is that
	// hint's model.
	Type    string
	ModelID string
}

// contributorType maps a Trace origin to an Agent Trace contributor type.
func contributorType(l Line) string {
	if l.Type != "" {
		return l.Type
	}
	switch l.Agent {
	case "human":
		return "human"
	case "", "baseline", "unknown":
		return "unknown"
	}
	return "ai"
}

// TurnURL is the conversation URL of a recorded turn.
func TurnURL(turnID string) string { return "trace://turn/" + turnID }

type convKey struct{ kind, turn, model string }

// FileRecord groups one file's per-line origins (index 0 is line first) into
// conversations of contiguous ranges. It returns the file and turn details for
// metadata.
func FileRecord(path string, first int, lines []Line, turns map[string]map[string]any) File {
	f := File{Path: path}
	index := map[convKey]int{}
	for i, l := range lines {
		k := convKey{contributorType(l), l.TurnID, l.ModelID}
		if k.kind != "ai" {
			k.turn = ""
		}
		at, ok := index[k]
		if !ok {
			c := Conversation{Contributor: &Contributor{Type: k.kind, ModelID: k.model}}
			if k.turn != "" {
				c.URL = TurnURL(k.turn)
				if turns != nil {
					turns[c.URL] = map[string]any{"agent": l.Agent, "step": l.Step}
				}
			}
			f.Conversations = append(f.Conversations, c)
			at = len(f.Conversations) - 1
			index[k] = at
		}
		line := first + i
		c := &f.Conversations[at]
		if n := len(c.Ranges); n > 0 && c.Ranges[n-1].EndLine == line-1 {
			c.Ranges[n-1].EndLine = line
		} else {
			c.Ranges = append(c.Ranges, Range{StartLine: line, EndLine: line})
		}
	}
	return f
}

// MergeFile appends other's conversations into f, merging equal ones.
func MergeFile(f *File, other File) {
	for _, oc := range other.Conversations {
		merged := false
		for i := range f.Conversations {
			c := &f.Conversations[i]
			if c.URL == oc.URL && *c.Contributor == *oc.Contributor {
				c.Ranges = append(c.Ranges, oc.Ranges...)
				merged = true
				break
			}
		}
		if !merged {
			f.Conversations = append(f.Conversations, oc)
		}
	}
}

// NewRecord builds a record. A non-empty revision yields a git vcs entry and
// an ID derived from it, so re-exporting the same commit is stable.
func NewRecord(revision string, at time.Time, files []File, metadata map[string]any) (Record, error) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	r := Record{Version: SpecVersion, Timestamp: at.UTC().Format(time.RFC3339), Tool: &Tool{Name: "trace"}, Files: files}
	if r.Files == nil {
		r.Files = []File{}
	}
	if len(metadata) > 0 {
		r.Metadata = map[string]any{"dev.usetrace": metadata}
	}
	var err error
	if revision != "" {
		r.VCS = &VCS{Type: "git", Revision: revision}
		sum := sha256.Sum256([]byte("trace-agent-trace:" + revision))
		r.ID = formatUUID(sum[:16])
	} else {
		r.ID, err = randomUUID()
	}
	return r, err
}

func formatUUID(raw []byte) string {
	var b [16]byte
	copy(b[:], raw)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return strings.Join([]string{s[:8], s[8:12], s[12:16], s[16:20], s[20:]}, "-")
}

func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("creating trace UUID: %w", err)
	}
	return formatUUID(b[:]), nil
}

// WriteJSONL writes records one per line.
func WriteJSONL(w io.Writer, records []Record) error {
	enc := json.NewEncoder(w)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return fmt.Errorf("writing Agent Trace: %w", err)
		}
	}
	return nil
}
