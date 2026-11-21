package gitx

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Read-only plumbing used to carry attribution across Git rewrites. None of
// these commands write to the repository.

// ValidSHA reports whether s is a full SHA-1 or SHA-256 object name.
func ValidSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ZeroSHA reports whether s is the all-zero object name Git uses for
// uncommitted lines.
func ZeroSHA(s string) bool { return ValidSHA(s) && strings.Trim(s, "0") == "" }

func gitCommand(root string, args ...string) *exec.Cmd {
	// Never prompt, never run fsmonitor or external diff programs on behalf
	// of an inspection.
	full := append([]string{"-C", root, "-c", "core.fsmonitor=false", "-c", "diff.external=", "--no-pager"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	return cmd
}

// boundedOutput runs cmd and returns at most limit bytes of its output.
// truncated reports that the output was longer.
func boundedOutput(cmd *exec.Cmd, limit int64) ([]byte, bool, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	b, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	truncated := int64(len(b)) > limit
	if truncated {
		_ = cmd.Process.Kill()
		b = b[:limit]
	}
	// Drain so the process can exit after a kill or a short read.
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	if truncated {
		return b, true, nil
	}
	if err := errors.Join(readErr, waitErr); err != nil {
		message := strings.TrimSpace(stderr.String())
		if len(message) > 200 {
			message = message[:200]
		}
		return nil, false, fmt.Errorf("%s: %w", message, err)
	}
	return b, false, nil
}

// ShowFile returns up to limit bytes of path as committed in rev.
func ShowFile(root, rev, path string, limit int64) ([]byte, bool, error) {
	if !ValidSHA(rev) {
		return nil, false, errors.New("invalid commit")
	}
	return boundedOutput(gitCommand(root, "cat-file", "blob", rev+":"+path), limit)
}

// Commit is the parsed header of a commit object.
type Commit struct {
	SHA       string
	Parents   []string
	Committed int64 // Unix seconds
	Message   string
}

// ReadCommit parses a commit object. The message is bounded.
func ReadCommit(root, sha string) (Commit, error) {
	if !ValidSHA(sha) {
		return Commit{}, errors.New("invalid commit")
	}
	b, _, err := boundedOutput(gitCommand(root, "cat-file", "commit", sha), 256<<10)
	if err != nil {
		return Commit{}, fmt.Errorf("reading commit: %w", err)
	}
	c := Commit{SHA: sha}
	header, message, _ := bytes.Cut(b, []byte("\n\n"))
	c.Message = string(message)
	for _, line := range strings.Split(string(header), "\n") {
		switch {
		case strings.HasPrefix(line, "parent "):
			c.Parents = append(c.Parents, strings.TrimPrefix(line, "parent "))
		case strings.HasPrefix(line, "committer "):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				c.Committed, _ = strconv.ParseInt(fields[len(fields)-2], 10, 64)
			}
		}
	}
	return c, nil
}

// PathChange is one file changed by a commit relative to its first parent.
type PathChange struct {
	Status  byte // A, M, D, R, C or T
	OldPath string
	Path    string
}

// ChangedPaths lists the files a non-merge commit changed, detecting renames.
func ChangedPaths(root, sha string) ([]PathChange, error) {
	if !ValidSHA(sha) {
		return nil, errors.New("invalid commit")
	}
	b, truncated, err := boundedOutput(gitCommand(root, "diff-tree", "-r", "-M", "--no-commit-id", "--name-status", "-z", "--root", sha), 8<<20)
	if err != nil {
		return nil, fmt.Errorf("listing commit changes: %w", err)
	}
	if truncated {
		return nil, errors.New("commit changes too many paths")
	}
	fields := strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
	var out []PathChange
	for i := 0; i < len(fields) && fields[i] != ""; {
		status := fields[i]
		i++
		change := PathChange{Status: status[0]}
		if change.Status == 'R' || change.Status == 'C' {
			if i+1 >= len(fields) {
				break
			}
			change.OldPath, change.Path = fields[i], fields[i+1]
			i += 2
		} else {
			if i >= len(fields) {
				break
			}
			change.OldPath, change.Path = fields[i], fields[i]
			i++
		}
		out = append(out, change)
	}
	return out, nil
}

// PatchID returns the stable patch ID of a non-merge commit's change against
// its first parent, or "" for an empty change.
func PatchID(root, sha string) (string, error) {
	if !ValidSHA(sha) {
		return "", errors.New("invalid commit")
	}
	diff, truncated, err := boundedOutput(gitCommand(root, "diff-tree", "-p", "--no-color", "--no-ext-diff", "--root", sha), 32<<20)
	if err != nil {
		return "", fmt.Errorf("reading commit patch: %w", err)
	}
	if truncated || len(bytes.TrimSpace(diff)) == 0 {
		return "", nil
	}
	cmd := gitCommand(root, "patch-id", "--stable")
	cmd.Stdin = bytes.NewReader(diff)
	out, _, err := boundedOutput(cmd, 4096)
	if err != nil {
		return "", fmt.Errorf("computing patch ID: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 || !ValidSHA(fields[0]) {
		return "", nil
	}
	return fields[0], nil
}

// IsAncestor reports whether a is an ancestor of (or equal to) b.
func IsAncestor(root, a, b string) bool {
	if !ValidSHA(a) || !ValidSHA(b) {
		return false
	}
	return gitCommand(root, "merge-base", "--is-ancestor", a, b).Run() == nil
}

// CommitExists reports whether sha names a commit in the repository.
func CommitExists(root, sha string) bool {
	if !ValidSHA(sha) {
		return false
	}
	return gitCommand(root, "cat-file", "-e", sha+"^{commit}").Run() == nil
}

// BlameLine says which commit last changed one line.
type BlameLine struct {
	// Commit is empty when the line is not committed yet.
	Commit string
	// Path and Line locate the line in Commit's version of the file.
	Path string
	Line int
}

// Blame attributes every line of path in rev, or of the working-tree file when
// rev is empty, to the commit that last changed it. Lines moved within the
// file are followed.
func Blame(root, rev, path string) ([]BlameLine, error) {
	args := []string{"blame", "--line-porcelain", "-M"}
	if rev != "" {
		if !ValidSHA(rev) {
			return nil, errors.New("invalid commit")
		}
		args = append(args, rev)
	}
	args = append(args, "--", path)
	b, truncated, err := boundedOutput(gitCommand(root, args...), 256<<20)
	if err != nil {
		return nil, fmt.Errorf("blaming %s: %w", path, err)
	}
	if truncated {
		return nil, errors.New("blame output too large")
	}
	var out []BlameLine
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	var current BlameLine
	expectHeader := true
	for scanner.Scan() {
		line := scanner.Text()
		if expectHeader {
			fields := strings.Fields(line)
			if len(fields) < 3 || !ValidSHA(fields[0]) {
				return nil, errors.New("unexpected blame output")
			}
			current = BlameLine{Commit: fields[0], Path: path}
			current.Line, err = strconv.Atoi(fields[1])
			if err != nil {
				return nil, errors.New("unexpected blame output")
			}
			if ZeroSHA(current.Commit) {
				current.Commit = ""
			}
			expectHeader = false
			continue
		}
		if strings.HasPrefix(line, "\t") {
			out = append(out, current)
			expectHeader = true
			continue
		}
		if name, ok := strings.CutPrefix(line, "filename "); ok {
			current.Path = name
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
