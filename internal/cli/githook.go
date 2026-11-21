package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/repo"
)

// maxRewritesPerEvent keeps each post-rewrite spool frame well below the
// daemon's frame limit; larger rebases are sent as several events.
const maxRewritesPerEvent = 2000

// gitHookEvents builds the events for Trace's Git hooks. They only read files
// in the Git directory, never run Git, and never fail the hook.
func gitHookEvents(hookEvent string, input []byte, root string) []event.Event {
	now := time.Now().UnixMilli()
	switch hookEvent {
	case string(event.GitCommit):
		ev, err := parseGenericHook("git", hookEvent, input, root)
		if err != nil || !event.ValidSHA(ev.GitSHA) {
			return nil
		}
		if gitDir, err := repo.GitDir(root); err == nil {
			ev.GitPrevious, ev.GitAction, ev.GitSources = commitContext(gitDir, ev.GitSHA)
		}
		return []event.Event{ev.Sanitized()}
	case string(event.GitRewrite):
		kind, body, _ := strings.Cut(string(input), "\n")
		kind = strings.TrimSpace(kind)
		var rewrites []event.Rewrite
		for _, line := range strings.Split(body, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && event.ValidSHA(fields[0]) && event.ValidSHA(fields[1]) {
				rewrites = append(rewrites, event.Rewrite{Old: fields[0], New: fields[1]})
			}
		}
		var events []event.Event
		for start := 0; start < len(rewrites); start += maxRewritesPerEvent {
			end := min(start+maxRewritesPerEvent, len(rewrites))
			ev := event.Event{Kind: event.GitRewrite, Agent: "git", Worktree: root, At: now, GitAction: kind, GitRewrites: rewrites[start:end]}
			events = append(events, ev.Sanitized())
		}
		return events
	}
	return nil
}

// commitContext reads what Git recorded about the commit that just happened:
// the reflog entry (previous HEAD and action, such as "commit (amend)") and
// the commit a rebase or cherry-pick was copying.
func commitContext(gitDir, sha string) (previous, action string, sources []string) {
	if line := lastLine(filepath.Join(gitDir, "logs", "HEAD"), 64<<10); line != "" {
		head, message, ok := strings.Cut(line, "\t")
		fields := strings.Fields(head)
		if ok && len(fields) >= 2 && fields[1] == sha {
			previous = fields[0]
			action, _, _ = strings.Cut(message, ": ")
		}
	}
	add := func(candidate string) {
		if event.ValidSHA(candidate) && candidate != sha {
			for _, existing := range sources {
				if existing == candidate {
					return
				}
			}
			sources = append(sources, candidate)
		}
	}
	if info, err := os.Lstat(filepath.Join(gitDir, "rebase-merge")); err == nil && info.IsDir() {
		// The last executed todo command ("pick <sha> ...", "squash <sha>",
		// "fixup -C <sha>") names the original commit.
		fields := strings.Fields(lastLine(filepath.Join(gitDir, "rebase-merge", "done"), 1<<20))
		for i := 1; i < len(fields); i++ {
			if !strings.HasPrefix(fields[i], "-") {
				add(fields[i])
				break
			}
		}
	}
	if b, err := readBounded(filepath.Join(gitDir, "CHERRY_PICK_HEAD"), 256); err == nil {
		add(strings.TrimSpace(string(b)))
	}
	return previous, action, sources
}

// lastLine returns the last complete line of a file, reading at most limit
// bytes from its end.
func lastLine(path string, limit int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	offset := max(info.Size()-limit, 0)
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return ""
	}
	b = bytes.TrimRight(b, "\n")
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		return string(b[i+1:])
	}
	if offset > 0 {
		return ""
	}
	return string(b)
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}
