package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Outcome is one reviewer's or the judge's run, as the renderers see it.
type Outcome struct {
	Agent  string
	Status string
	// Raw is set when the answer did not follow the findings schema.
	Raw string
	// Empty marks a completed run that returned no answer at all.
	Empty bool
}

// Report is what SARIF and Markdown render: the consolidated findings and
// whatever could not be structured.
type Report struct {
	Reviewers []string
	Judge     string
	// Source is "judge" when the judge's answer was used, otherwise "merged".
	Source   string
	Files    []FileDiff
	Findings []Finding
	Outcomes []Outcome
}

// SARIF rule identifiers.
const (
	RuleFinding      = "trace-review/finding"
	RuleUnstructured = "trace-review/unstructured"
	maxSARIFRawBytes = 32 << 10
)

// SARIFLevel maps a severity onto a SARIF result level.
func SARIFLevel(severity string) string {
	switch severity {
	case Critical, High:
		return "error"
	case Medium:
		return "warning"
	default:
		return "note"
	}
}

type sarifMessage struct {
	Text string `json:"text"`
}

// SARIF renders the report as a SARIF 2.1.0 log.
func SARIF(r Report) ([]byte, error) {
	results := []map[string]any{}
	for _, f := range r.Findings {
		text := f.Title
		if f.Detail != "" {
			text += "\n\n" + f.Detail
		}
		result := map[string]any{
			"ruleId":    RuleFinding,
			"ruleIndex": 0,
			"level":     SARIFLevel(f.Severity),
			"message":   sarifMessage{text},
			"partialFingerprints": map[string]string{
				"traceReviewFinding/v1": fingerprint(f.File, titleKey(f.Title)),
			},
			"properties": map[string]any{
				"severity":  f.Severity,
				"reviewers": nonNil(f.Reviewers),
				"agreement": f.Agreement,
			},
		}
		if f.File != "" {
			region := map[string]int{}
			if f.StartLine > 0 {
				region["startLine"] = f.StartLine
				region["endLine"] = f.EndLine
			}
			physical := map[string]any{"artifactLocation": map[string]string{"uri": fileURI(f.File), "uriBaseId": "%SRCROOT%"}}
			if len(region) > 0 {
				physical["region"] = region
			}
			result["locations"] = []map[string]any{{"physicalLocation": physical}}
		}
		results = append(results, result)
	}
	var notifications []map[string]any
	successful := true
	for _, o := range r.Outcomes {
		if o.Status != "completed" {
			successful = false
			notifications = append(notifications, map[string]any{
				"level":   "error",
				"message": sarifMessage{fmt.Sprintf("%s review %s", o.Agent, o.Status)},
			})
		}
		if o.Empty {
			notifications = append(notifications, map[string]any{
				"level":   "warning",
				"message": sarifMessage{fmt.Sprintf("%s returned no answer", o.Agent)},
			})
		}
		if o.Raw != "" {
			results = append(results, map[string]any{
				"ruleId":     RuleUnstructured,
				"ruleIndex":  1,
				"level":      "note",
				"message":    sarifMessage{fmt.Sprintf("Unstructured review from %s:\n\n%s", o.Agent, clip(o.Raw, maxSARIFRawBytes))},
				"properties": map[string]any{"reviewers": []string{o.Agent}},
			})
		}
	}
	invocation := map[string]any{"executionSuccessful": successful}
	if len(notifications) > 0 {
		invocation["toolExecutionNotifications"] = notifications
	}
	run := map[string]any{
		"tool": map[string]any{"driver": map[string]any{
			"name":           "trace review",
			"informationUri": "https://usetrace.si",
			"rules": []map[string]any{
				{"id": RuleFinding, "name": "ReviewFinding", "shortDescription": sarifMessage{"Finding from an independent agent code review"}},
				{"id": RuleUnstructured, "name": "UnstructuredReview", "shortDescription": sarifMessage{"Review answer that did not follow the findings schema"}},
			},
		}},
		"invocations": []map[string]any{invocation},
		"results":     results,
		"properties": map[string]any{
			"reviewers":       nonNil(r.Reviewers),
			"judge":           r.Judge,
			"findingsSource":  r.Source,
			"changedFiles":    len(r.Files),
			"structuredCount": len(r.Findings),
		},
	}
	log := map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs":    []map[string]any{run},
	}
	b, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding SARIF: %w", err)
	}
	return append(b, '\n'), nil
}

// fileURI percent-encodes each path segment so the result is a valid
// relative URI reference.
func fileURI(file string) string {
	segments := strings.Split(file, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

func fingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Markdown renders the report as a pull request comment. Model text is
// escaped so it cannot inject HTML, links, mentions or table cells.
func Markdown(r Report) string {
	var b strings.Builder
	b.WriteString("## Trace review\n\n")
	added, removed := 0, 0
	for _, f := range r.Files {
		added += f.Additions()
		removed += f.Deletions()
	}
	fmt.Fprintf(&b, "Reviewed %d changed %s (+%d −%d) with %s", len(r.Files), plural(len(r.Files), "file", "files"), added, removed, mdCodeList(r.Reviewers))
	if r.Judge != "" {
		fmt.Fprintf(&b, "; consolidated by `%s`", mdCode(r.Judge))
		if r.Source != "judge" {
			b.WriteString(" (judge answer unavailable; reviewer findings merged without it)")
		}
	}
	b.WriteString(".\n\n")
	if len(r.Findings) == 0 {
		b.WriteString("No structured findings.\n")
	} else {
		b.WriteString("| Severity | Location | Finding | Agreement |\n|---|---|---|---|\n")
		for _, f := range r.Findings {
			location := "—"
			if f.Location() != "" {
				location = "`" + mdCode(f.Location()) + "`"
			}
			agreement := fmt.Sprintf("%d/%d", f.Agreement, len(r.Reviewers))
			if len(f.Reviewers) > 0 {
				agreement += " " + mdCodeList(f.Reviewers)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", f.Severity, location, mdCell(f.Title), agreement)
		}
		for _, f := range r.Findings {
			if f.Detail == "" {
				continue
			}
			summary := f.Severity + ": " + f.Title
			if f.Location() != "" {
				summary += " (" + f.Location() + ")"
			}
			fmt.Fprintf(&b, "\n<details><summary>%s</summary>\n\n%s\n\n</details>\n", htmlInline(summary), mdText(f.Detail))
		}
	}
	for _, o := range r.Outcomes {
		if o.Status != "completed" {
			fmt.Fprintf(&b, "\n> **%s review %s.**\n", mdInline(o.Agent), mdInline(o.Status))
		}
		if o.Empty {
			fmt.Fprintf(&b, "\n> **%s returned no answer.**\n", mdInline(o.Agent))
		}
		if o.Raw != "" {
			fence := strings.Repeat("`", max(3, longestRun(o.Raw, '`')+1))
			fmt.Fprintf(&b, "\n<details><summary>Unstructured answer from %s</summary>\n\n%stext\n%s\n%s\n\n</details>\n", htmlInline(o.Agent), fence, strings.TrimRight(o.Raw, "\n"), fence)
		}
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func mdCodeList(names []string) string {
	if len(names) == 0 {
		return ""
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "`" + mdCode(n) + "`"
	}
	return strings.Join(quoted, ", ")
}

// mdCode makes text safe inside a single-backtick code span in a table.
func mdCode(s string) string {
	return strings.NewReplacer("`", "'", "|", "\\|", "\n", " ", "\r", " ").Replace(s)
}

var mdEscaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;",
	`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`,
	"|", `\|`, "~", `\~`, "!", `\!`,
	// A zero-width joiner keeps @name from notifying anyone, and keeps
	// GitHub from turning bare URLs, www. hosts, e-mail addresses and #123
	// references into links.
	"@", "@\u200d", "://", "://\u200d", "www.", "www\u200d.", "#", "#\u200d",
)

// mdInline escapes one line of model text.
func mdInline(s string) string {
	return mdEscaper.Replace(oneLine(s))
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "[", "&#91;", "]", "&#93;",
	"@", "@\u200d", "://", "://\u200d", "www.", "www\u200d.", "#", "#\u200d")

// htmlInline escapes one line of model text inside an HTML block, where
// Markdown escapes are not processed.
func htmlInline(s string) string {
	return htmlEscaper.Replace(oneLine(s))
}

func mdCell(s string) string {
	return mdInline(s)
}

// mdText escapes model prose, keeping its line breaks.
func mdText(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, line := range lines {
		line = mdEscaper.Replace(strings.TrimRight(line, "\r"))
		// Leading markers would start lists, quotes or headings.
		if trimmed := strings.TrimLeft(line, " "); strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "=") {
			line = strings.Repeat(" ", len(line)-len(trimmed)) + `\` + trimmed
		}
		lines[i] = line
	}
	return strings.Join(lines, "  \n")
}

func longestRun(s string, c byte) int {
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

// terminalControl matches ANSI escape sequences (CSI and OSC) and the
// characters that can rewrite or reorder a terminal line: C0 controls other
// than tab and newline, DEL, C1 controls and bidirectional overrides.
var terminalControl = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)?|[\x00-\x08\x0b-\x1f\x7f\u0080-\u009f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]")

// Printable removes terminal control sequences from model text before it is
// printed as plain text.
func Printable(s string) string {
	return terminalControl.ReplaceAllString(s, "")
}
