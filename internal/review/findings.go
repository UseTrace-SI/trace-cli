// Package review holds the structured side of `trace review`: the findings
// schema reviewers and the judge are asked to return, its tolerant parser,
// the judge prompt, diff summaries and the SARIF and Markdown renderings.
package review

import (
	"bytes"
	"encoding/json"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

// Severities, most severe first.
const (
	Critical = "critical"
	High     = "high"
	Medium   = "medium"
	Low      = "low"
	Info     = "info"
)

var severityRank = map[string]int{Critical: 0, High: 1, Medium: 2, Low: 3, Info: 4}

// Bounds applied to every parsed finding.
const (
	MaxFindings    = 200
	MaxTitleBytes  = 200
	MaxDetailBytes = 4000
)

// Finding is one review finding. Lines are 1-based and inclusive on the new
// side of the diff; 0 means the finding has no line range. Reviewers lists
// the reviewers that reported it and Agreement is their number.
type Finding struct {
	File      string   `json:"file,omitempty"`
	StartLine int      `json:"start_line,omitempty"`
	EndLine   int      `json:"end_line,omitempty"`
	Severity  string   `json:"severity"`
	Title     string   `json:"title"`
	Detail    string   `json:"detail,omitempty"`
	Reviewers []string `json:"reviewers,omitempty"`
	Agreement int      `json:"agreement,omitempty"`
}

// SchemaInstructions tells a reviewer how to answer. Reviewers report their
// own findings, so they leave out the reviewers field.
const SchemaInstructions = `Respond with only one JSON object and no other text, in this schema:
{"findings": [{"file": "path/relative/to/repo", "start_line": 1, "end_line": 1, "severity": "critical|high|medium|low|info", "title": "one-line summary", "detail": "explanation, evidence and uncertainty"}]}
Use lines from the new side of the diff, omit start_line and end_line when a finding has no specific lines, and return {"findings": []} when there is nothing to report.`

// JudgeSchemaInstructions extends the schema with the agreeing reviewers.
const JudgeSchemaInstructions = `Respond with only one JSON object and no other text, in this schema:
{"findings": [{"file": "path/relative/to/repo", "start_line": 1, "end_line": 1, "severity": "critical|high|medium|low|info", "title": "one-line summary", "detail": "explanation, evidence and uncertainty", "reviewers": ["names of every reviewer that reported this finding"]}]}
Return {"findings": []} when no finding survives.`

// flexInt accepts a JSON number or a numeric string, which models emit
// interchangeably.
type flexInt int

func (n *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1e9 {
		*n = 0
		return nil
	}
	*n = flexInt(f)
	return nil
}

type rawFinding struct {
	File      string   `json:"file"`
	Path      string   `json:"path"`
	Line      flexInt  `json:"line"`
	StartLine flexInt  `json:"start_line"`
	EndLine   flexInt  `json:"end_line"`
	Severity  string   `json:"severity"`
	Title     string   `json:"title"`
	Detail    string   `json:"detail"`
	Reviewers []string `json:"reviewers"`
}

// Parse extracts findings from a model's answer. It accepts the object
// {"findings": [...]} or a bare array, alone, inside a fenced code block, or
// surrounded by prose. ok is false when no such value is found, and callers
// then fall back to the raw text.
func Parse(output string) (findings []Finding, ok bool) {
	for _, candidate := range jsonCandidates(output) {
		if raws, ok := decodeFindings(candidate); ok {
			out := make([]Finding, 0, len(raws))
			for _, raw := range raws {
				if f, ok := normalize(raw); ok {
					out = append(out, f)
				}
				if len(out) == MaxFindings {
					break
				}
			}
			return out, true
		}
	}
	return nil, false
}

// jsonCandidates lists the texts worth decoding, most likely first: the
// whole answer, each fenced block, then the text from each opening brace or
// bracket (bounded, so a long answer cannot cause quadratic work).
func jsonCandidates(output string) []string {
	output = strings.TrimSpace(output)
	out := []string{output}
	rest := output
	for {
		start := strings.Index(rest, "```")
		if start < 0 {
			break
		}
		body := rest[start+3:]
		if nl := strings.IndexByte(body, '\n'); nl >= 0 {
			body = body[nl+1:]
		}
		end := strings.Index(body, "```")
		if end < 0 {
			break
		}
		out = append(out, body[:end])
		rest = body[end+3:]
	}
	for i, tries := 0, 0; i < len(output) && tries < 32; i++ {
		if output[i] == '{' || output[i] == '[' {
			out = append(out, output[i:])
			tries++
		}
	}
	return out
}

func decodeFindings(text string) ([]rawFinding, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var object struct {
		Findings *[]json.RawMessage `json:"findings"`
	}
	var items []json.RawMessage
	if value[0] == '{' {
		if err := json.Unmarshal(value, &object); err != nil || object.Findings == nil {
			return nil, false
		}
		items = *object.Findings
	} else if err := json.Unmarshal(value, &items); err != nil {
		return nil, false
	}
	raws := make([]rawFinding, 0, len(items))
	for _, item := range items {
		var raw rawFinding
		if json.Unmarshal(item, &raw) == nil {
			raws = append(raws, raw)
		}
	}
	// A bare array of something else (e.g. a list of strings in prose) is
	// not a findings answer.
	if value[0] == '[' && len(items) > 0 && len(raws) == 0 {
		return nil, false
	}
	return raws, true
}

func normalize(raw rawFinding) (Finding, bool) {
	f := Finding{
		File:     cleanFile(firstNonEmpty(raw.File, raw.Path)),
		Severity: NormalizeSeverity(raw.Severity),
		Title:    clip(oneLine(raw.Title), MaxTitleBytes),
		Detail:   clip(strings.TrimSpace(raw.Detail), MaxDetailBytes),
	}
	if f.Title == "" {
		f.Title = clip(oneLine(f.Detail), MaxTitleBytes)
	}
	if f.Title == "" {
		return Finding{}, false
	}
	f.StartLine, f.EndLine = int(raw.StartLine), int(raw.EndLine)
	if f.StartLine == 0 {
		f.StartLine = int(raw.Line)
	}
	if f.StartLine == 0 {
		f.EndLine = 0
	} else if f.EndLine < f.StartLine {
		f.EndLine = f.StartLine
	}
	if f.File == "" {
		f.StartLine, f.EndLine = 0, 0
	}
	f.Reviewers = raw.Reviewers
	return f, true
}

// NormalizeSeverity maps a model's severity word onto the five levels.
// Unknown words become medium so that they are neither hidden nor inflated.
func NormalizeSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical", "blocker", "severe":
		return Critical
	case "high", "major", "error":
		return High
	case "low", "minor":
		return Low
	case "info", "informational", "note", "nit", "nitpick", "suggestion":
		return Info
	default:
		return Medium
	}
}

// cleanFile keeps repository-relative paths and drops anything that would
// point outside the repository.
func cleanFile(file string) string {
	file = strings.TrimSpace(strings.ReplaceAll(file, `\`, "/"))
	if file == "" || strings.HasPrefix(file, "/") || strings.ContainsAny(file, "\x00\n\r") || (len(file) > 1 && file[1] == ':') {
		return ""
	}
	file = path.Clean(strings.TrimPrefix(file, "./"))
	if file == "." || file == ".." || strings.HasPrefix(file, "../") || len(file) > 4096 {
		return ""
	}
	// A scheme or drive ("https://…", "file:x", "c:x") is not a repository
	// path, and a secret path is never reported.
	if first, _, _ := strings.Cut(file, "/"); strings.Contains(first, ":") || manifest.Excluded(file) {
		return ""
	}
	return file
}

// Constrain keeps findings inside the reviewed change. lines maps each
// reviewed path to its line count on the new side (0 when it has none, such
// as a deleted or binary file). A finding on any other path loses its
// location, which is kept in its detail as text; line ranges are clamped to
// the file, and a range that starts past its end is dropped.
func Constrain(findings []Finding, lines map[string]int) []Finding {
	for i := range findings {
		f := &findings[i]
		if f.File == "" {
			continue
		}
		n, reviewed := lines[f.File]
		if !reviewed {
			note := "Reported location (not a reviewed file): " + f.Location()
			if f.Detail != "" {
				note += "\n\n" + f.Detail
			}
			f.Detail = clip(note, MaxDetailBytes)
			f.File, f.StartLine, f.EndLine = "", 0, 0
			continue
		}
		if f.StartLine > n {
			f.StartLine, f.EndLine = 0, 0
		} else if f.EndLine > n {
			f.EndLine = n
		}
	}
	return findings
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clip shortens s to at most n bytes on a rune boundary, marking the cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= len("…") {
		return ""
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// Attribute sets every finding's reviewers to the one reviewer that wrote it.
func Attribute(findings []Finding, reviewer string) []Finding {
	for i := range findings {
		findings[i].Reviewers = []string{reviewer}
		findings[i].Agreement = 1
	}
	return findings
}

// Reconcile keeps only reviewer names the judge was given, in their given
// order, and recounts agreement. A judge cannot invent reviewers.
func Reconcile(findings []Finding, reviewers []string) []Finding {
	for i := range findings {
		claimed := map[string]bool{}
		for _, name := range findings[i].Reviewers {
			claimed[strings.TrimSpace(name)] = true
		}
		var kept []string
		for _, name := range reviewers {
			if claimed[name] {
				kept = append(kept, name)
			}
		}
		findings[i].Reviewers = kept
		findings[i].Agreement = len(kept)
	}
	Sort(findings)
	return findings
}

// Merge consolidates reviewers' findings without a judge: findings on the
// same file with overlapping lines and the same normalized title are one
// finding, which keeps the highest severity and every reviewer.
func Merge(perReviewer [][]Finding) []Finding {
	var merged []Finding
	for _, findings := range perReviewer {
	next:
		for _, f := range findings {
			for i := range merged {
				m := &merged[i]
				if m.File == f.File && overlaps(*m, f) && titleKey(m.Title) == titleKey(f.Title) {
					if severityRank[f.Severity] < severityRank[m.Severity] {
						m.Severity = f.Severity
					}
					for _, name := range f.Reviewers {
						if !contains(m.Reviewers, name) {
							m.Reviewers = append(m.Reviewers, name)
						}
					}
					m.Agreement = len(m.Reviewers)
					continue next
				}
			}
			f.Reviewers = append([]string(nil), f.Reviewers...)
			f.Agreement = len(f.Reviewers)
			merged = append(merged, f)
		}
	}
	Sort(merged)
	if merged == nil {
		merged = []Finding{}
	}
	return merged
}

func overlaps(a, b Finding) bool {
	if a.StartLine == 0 || b.StartLine == 0 {
		return a.StartLine == b.StartLine
	}
	return a.StartLine <= b.EndLine && b.StartLine <= a.EndLine
}

func titleKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Sort orders findings by severity, then agreement, then location.
func Sort(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] < severityRank[b.Severity]
		}
		if a.Agreement != b.Agreement {
			return a.Agreement > b.Agreement
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.StartLine < b.StartLine
	})
}

// Location renders file:start-end, file:line or file.
func (f Finding) Location() string {
	switch {
	case f.File == "":
		return ""
	case f.StartLine == 0:
		return f.File
	case f.EndLine > f.StartLine:
		return f.File + ":" + strconv.Itoa(f.StartLine) + "-" + strconv.Itoa(f.EndLine)
	default:
		return f.File + ":" + strconv.Itoa(f.StartLine)
	}
}
