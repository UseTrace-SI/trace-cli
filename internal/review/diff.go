package review

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Hunk is one diff hunk. Added holds the new-side line numbers it adds.
type Hunk struct {
	NewStart, NewCount int
	Added              []int
	Removed            int
}

// FileDiff is one changed path in a diff.
type FileDiff struct {
	Path   string
	Binary bool
	Hunks  []Hunk
}

// Additions counts added lines across hunks.
func (f FileDiff) Additions() int {
	n := 0
	for _, h := range f.Hunks {
		n += len(h.Added)
	}
	return n
}

// Deletions counts removed lines across hunks.
func (f FileDiff) Deletions() int {
	n := 0
	for _, h := range f.Hunks {
		n += h.Removed
	}
	return n
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ParseDiff splits `git diff --no-renames` output for paths, which must be
// the same paths in the order `git diff --name-only` listed them. Git quotes
// unusual file names in headers, so sections are matched to paths by
// position rather than by parsing names; ok is false when the counts differ.
func ParseDiff(diff string, paths []string) (files []FileDiff, ok bool) {
	sections := 0
	var current *FileDiff
	inHunk := false
	newLine := 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			if sections == len(paths) {
				return nil, false
			}
			files = append(files, FileDiff{Path: paths[sections]})
			current = &files[len(files)-1]
			sections++
			inHunk = false
			continue
		}
		if current == nil {
			continue
		}
		if m := hunkHeader.FindStringSubmatch(line); m != nil {
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			current.Hunks = append(current.Hunks, Hunk{NewStart: start, NewCount: count})
			inHunk = true
			newLine = start
			continue
		}
		if !inHunk {
			if strings.HasPrefix(line, "Binary files ") || line == "GIT binary patch" {
				current.Binary = true
			}
			continue
		}
		hunk := &current.Hunks[len(current.Hunks)-1]
		switch {
		case strings.HasPrefix(line, "+"):
			hunk.Added = append(hunk.Added, newLine)
			newLine++
		case strings.HasPrefix(line, "-"):
			hunk.Removed++
		case strings.HasPrefix(line, " "):
			newLine++
		}
	}
	if sections != len(paths) {
		return nil, false
	}
	return files, true
}

// Summary renders one line per file: its path and added/removed counts.
func Summary(files []FileDiff) string {
	var b strings.Builder
	added, removed := 0, 0
	for _, f := range files {
		if f.Binary {
			fmt.Fprintf(&b, "%s (binary)\n", f.Path)
			continue
		}
		fmt.Fprintf(&b, "%s +%d -%d\n", f.Path, f.Additions(), f.Deletions())
		added += f.Additions()
		removed += f.Deletions()
	}
	fmt.Fprintf(&b, "%d %s changed, +%d -%d\n", len(files), plural(len(files), "file", "files"), added, removed)
	return b.String()
}
