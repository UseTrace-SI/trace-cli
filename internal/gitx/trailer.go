package gitx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
)

// TrailerKey is the commit trailer that names a recorded turn whose edits a
// commit contains. Its value is the turn's ULID; it is added only when the
// repository opts in with `trace config set commit-trailer on`.
const TrailerKey = "Trace-Turn"

const trailerMarker = "commit-trailer"

// MaxTrailers bounds the Trace-Turn trailers added to one commit.
const MaxTrailers = 20

var prepareCommitMsgHook = hookSpec{
	name:  "prepare-commit-msg",
	begin: "# trace prepare-commit-msg begin\n",
	end:   "# trace prepare-commit-msg end\n",
	body: func() string {
		// Git runs hooks from the top of the worktree. Without the opt-in
		// marker the block does nothing and never starts Trace; it never fails
		// the commit.
		return "if [ -f .trace/" + trailerMarker + " ]; then\n" +
			"\t" + hookcmd.TracePath() + " commit-trailer-hook \"$1\" \"$2\" >/dev/null 2>&1 || true\n" +
			"fi\n"
	},
	// A failing original prepare-commit-msg hook aborts the commit, as before.
	chain: "if [ -x \"$0.pre-trace\" ]; then \"$0.pre-trace\" \"$@\" || exit $?; fi\n",
}

// TrailerEnabled reports whether the repository opted into commit trailers.
func TrailerEnabled(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".trace", trailerMarker))
	return err == nil
}

// EnableTrailer writes the opt-in marker and merges the prepare-commit-msg
// block into the repository's hook.
func EnableTrailer(root string) error {
	dir := filepath.Join(root, ".trace")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, trailerMarker), []byte("1\n"), 0600); err != nil {
		return err
	}
	return installHook(root, prepareCommitMsgHook)
}

// DisableTrailer removes the marker and the prepare-commit-msg block,
// restoring a hook that was preserved during installation.
func DisableTrailer(root string) error {
	err := os.Remove(filepath.Join(root, ".trace", trailerMarker))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return uninstallHook(root, prepareCommitMsgHook)
}

var ulid = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// AddTrailers appends a Trace-Turn trailer for each turn to the commit message
// file with `git interpret-trailers`, which keeps existing trailers and skips
// a turn the message already names.
func AddTrailers(root, msgFile string, turns []string) error {
	if len(turns) > MaxTrailers {
		turns = turns[:MaxTrailers]
	}
	args := []string{"interpret-trailers", "--in-place", "--if-exists", "addIfDifferent"}
	n := 0
	for _, t := range turns {
		if !ulid.MatchString(t) {
			continue
		}
		args = append(args, "--trailer", TrailerKey+": "+t)
		n++
	}
	if n == 0 {
		return nil
	}
	args = append(args, "--", msgFile)
	if out, err := gitCommand(root, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("adding commit trailers: %w: %s", err, out)
	}
	return nil
}
