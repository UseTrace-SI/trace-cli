package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/UseTrace-SI/trace-cli/internal/attribution"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/redact"
	"github.com/UseTrace-SI/trace-cli/internal/review"
)

// Caps on the provenance given to reviewers.
const (
	maxProvenancePromptBytes = 1 << 10
	maxProvenanceBytes       = 16 << 10
	maxProvenanceStepsByHunk = 2
)

type provenanceEntry struct {
	text  string
	shows []int64
}

type hunkTurn struct {
	step   int64
	agent  string
	prompt string
	lines  int
}

// reviewProvenance describes, for each changed hunk, the recorded turns that
// wrote its added lines and their prompts, redacted and capped. Lines are
// explained like `trace why`; a file whose reviewed content cannot be
// explained (binary, too large, a symlink, or staged content that differs
// from the working tree) is listed as unavailable.
func reviewProvenance(root string, staged bool, files []review.FileDiff) (string, error) {
	e, err := engine.Open(root)
	if err != nil {
		return "", err
	}
	defer e.Close()
	var b strings.Builder
	shown := map[int64]bool{}
	omitted := 0
	redactor := redact.Redactor{}
	for _, f := range files {
		var added []int
		for _, h := range f.Hunks {
			added = append(added, h.Added...)
		}
		if len(added) == 0 {
			continue
		}
		// Each entry is redacted whole (paths and agent names included),
		// then written if it fits; a prompt counts as shown only once its
		// entry is written, so later hunks can refer to it.
		write := func(entry provenanceEntry) {
			text := redactor.Text(entry.text)
			if b.Len()+len(text) > maxProvenanceBytes {
				omitted++
				return
			}
			b.WriteString(text)
			for _, step := range entry.shows {
				shown[step] = true
			}
		}
		current, reason := reviewedContent(root, f.Path, staged, f.Binary)
		if reason != "" {
			write(provenanceEntry{text: fmt.Sprintf("%s: provenance unavailable (%s)\n", f.Path, reason)})
			continue
		}
		origins, err := attribution.ExplainLines(e.DB, e.Blobs, root, f.Path, current, added, false)
		if err != nil {
			// Provenance is context for reviewers; one unexplainable file
			// should not stop the review.
			write(provenanceEntry{text: fmt.Sprintf("%s: provenance unavailable (history lookup failed)\n", f.Path)})
			continue
		}
		byLine := map[int]attribution.Origin{}
		for i, line := range added {
			if i < len(origins) && len(origins[i]) > 0 {
				byLine[line] = origins[i][len(origins[i])-1]
			}
		}
		for _, h := range f.Hunks {
			if len(h.Added) > 0 {
				write(hunkProvenance(f.Path, h, byLine, shown, redactor))
			}
		}
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(%d more hunks omitted: provenance is capped at %d KiB)\n", omitted, maxProvenanceBytes>>10)
	}
	if b.Len() == 0 {
		b.WriteString("(no added lines to explain)\n")
	}
	return b.String(), nil
}

// hunkProvenance renders the turns behind one hunk's added lines, most lines
// first. A prompt already shown is referred to; the steps whose prompts this
// entry shows are returned with it.
func hunkProvenance(path string, h review.Hunk, byLine map[int]attribution.Origin, shown map[int64]bool, redactor redact.Redactor) provenanceEntry {
	turns := map[int64]*hunkTurn{}
	unknown := 0
	for _, line := range h.Added {
		origin, ok := byLine[line]
		if !ok || origin.Step < 1 {
			unknown++
			continue
		}
		t := turns[origin.Step]
		if t == nil {
			t = &hunkTurn{step: origin.Step, agent: origin.Agent, prompt: origin.Prompt}
			turns[origin.Step] = t
		}
		t.lines++
	}
	ordered := make([]*hunkTurn, 0, len(turns))
	for _, t := range turns {
		ordered = append(ordered, t)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].lines != ordered[j].lines {
			return ordered[i].lines > ordered[j].lines
		}
		return ordered[i].step < ordered[j].step
	})
	first, last := h.Added[0], h.Added[len(h.Added)-1]
	label := fmt.Sprintf("%s lines %d-%d", path, first, last)
	if first == last {
		label = fmt.Sprintf("%s line %d", path, first)
	}
	var b strings.Builder
	var shows []int64
	if len(ordered) == 0 {
		return provenanceEntry{text: label + ": no recorded turn\n"}
	}
	for i, t := range ordered {
		if i == maxProvenanceStepsByHunk {
			fmt.Fprintf(&b, "%s: %d more turns\n", label, len(ordered)-i)
			break
		}
		fmt.Fprintf(&b, "%s: step %d by %s (%d of %d added lines)", label, t.step, oneLine(t.agent), t.lines, len(h.Added))
		switch {
		case shown[t.step]:
			b.WriteString(", prompt shown above\n")
		case strings.TrimSpace(t.prompt) == "":
			b.WriteString(", no prompt recorded\n")
		default:
			shows = append(shows, t.step)
			// Redact before clipping so a cut never leaves part of a secret.
			fmt.Fprintf(&b, ", prompt: %q\n", clipBytes(oneLine(redactor.Text(t.prompt)), maxProvenancePromptBytes))
		}
	}
	if unknown > 0 {
		fmt.Fprintf(&b, "%s: %d added lines without a recorded turn\n", label, unknown)
	}
	return provenanceEntry{text: b.String(), shows: shows}
}

// reviewedContent returns the content the diff's new side describes: the
// working tree file, or for --staged the index entry when it matches the
// working tree (provenance explains working-tree lines).
func reviewedContent(root, path string, staged, binary bool) (string, string) {
	if binary {
		return "", "binary file"
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() {
		return "", "not a regular file in the working tree"
	}
	if info.Size() > attribution.MaxTextBytes {
		return "", "file too large"
	}
	f, err := os.Open(full)
	if err != nil {
		return "", "unreadable"
	}
	data, err := io.ReadAll(io.LimitReader(f, attribution.MaxTextBytes+1))
	if err := errors.Join(err, f.Close()); err != nil || len(data) > attribution.MaxTextBytes {
		return "", "unreadable"
	}
	if staged {
		// Stage 0 explicitly, so a path such as "1:x" is not read as a stage.
		index, err := gitBlobLimited(root, ":0:"+path, attribution.MaxTextBytes)
		if err != nil || index != string(data) {
			return "", "staged content differs from the working tree"
		}
	}
	return string(data), ""
}

// gitBlobLimited reads a blob, failing when it exceeds limit bytes.
func gitBlobLimited(root, object string, limit int64) (string, error) {
	cmd := exec.Command("git", "-C", root, "cat-file", "blob", object)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	if int64(len(data)) > limit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", errors.New("blob too large")
	}
	if err := errors.Join(readErr, cmd.Wait()); err != nil {
		return "", err
	}
	return string(data), nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clipBytes shortens s to at most n bytes on a rune boundary.
func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
