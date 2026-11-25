package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/gitx"
	"github.com/spf13/cobra"
)

func configCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Read or change repository settings"}
	report := func(c *cobra.Command, key string, on bool) error {
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"key": key, "value": onOff(on)})
		}
		_, err := fmt.Fprintf(c.OutOrStdout(), "%s %s\n", key, onOff(on))
		return err
	}
	cmd.AddCommand(&cobra.Command{Use: "get commit-trailer", Short: "Show a setting", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if args[0] != "commit-trailer" {
			return fmt.Errorf("unknown setting %q (known: commit-trailer)", args[0])
		}
		return report(c, args[0], gitx.TrailerEnabled(root))
	}})
	cmd.AddCommand(&cobra.Command{Use: "set commit-trailer on|off", Short: "Change a setting", Args: cobra.ExactArgs(2), RunE: func(c *cobra.Command, args []string) error {
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		if args[0] != "commit-trailer" {
			return fmt.Errorf("unknown setting %q (known: commit-trailer)", args[0])
		}
		var on bool
		switch strings.ToLower(args[1]) {
		case "on", "true", "1":
			on = true
			err = gitx.EnableTrailer(root)
		case "off", "false", "0":
			err = gitx.DisableTrailer(root)
		default:
			return fmt.Errorf("value must be on or off")
		}
		var manual *gitx.ManualHookError
		if errors.As(err, &manual) {
			for _, h := range manual.Hooks {
				if _, werr := fmt.Fprintf(c.ErrOrStderr(), "Add to %s:\n%s", h.Path, h.Block); werr != nil {
					return werr
				}
			}
		} else if err != nil {
			return err
		}
		return report(c, args[0], on)
	}})
	return cmd
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// commitTrailerHookCommand runs from the prepare-commit-msg hook. It never
// fails: a commit must not depend on Trace.
func commitTrailerHookCommand(repoPath *string) *cobra.Command {
	return &cobra.Command{Use: "commit-trailer-hook MSGFILE [SOURCE]", Hidden: true, Args: cobra.RangeArgs(1, 3),
		SilenceErrors: true, SilenceUsage: true, RunE: func(_ *cobra.Command, args []string) error {
			source := ""
			if len(args) > 1 {
				source = args[1]
			}
			_ = addCommitTrailers(*repoPath, args[0], source)
			return nil
		}}
}

func addCommitTrailers(repoPath, msgFile, source string) error {
	if source == "merge" || source == "squash" {
		return nil
	}
	root, err := initializedRoot(repoPath)
	if err != nil || !gitx.TrailerEnabled(root) {
		return err
	}
	turns, err := stagedTurns(root)
	if err != nil || len(turns) == 0 {
		return err
	}
	return gitx.AddTrailers(root, msgFile, turns)
}

// stagedTurns lists, oldest first, recorded turns that edited a staged path
// after the current HEAD commit was made (all recorded turns on an unborn
// branch).
func stagedTurns(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "diff", "--cached", "--name-only", "-z", "--no-renames").Output()
	if err != nil {
		return nil, fmt.Errorf("listing staged paths: %w", err)
	}
	paths := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(paths) == 0 || paths[0] == "" {
		return nil, nil
	}
	e, err := engine.Open(root)
	if err != nil {
		return nil, err
	}
	defer e.Close()
	since := int64(-1)
	if head, err := gitx.Head(root); err == nil {
		// The post-commit hook records when HEAD was committed, to the
		// millisecond; otherwise fall back to the start of Git's one-second
		// commit time.
		if e.DB.QueryRow("SELECT at FROM commits WHERE sha=?", head).Scan(&since) != nil {
			if ct, err := exec.Command("git", "-C", root, "log", "-1", "--format=%ct", head).Output(); err == nil {
				if sec, err := strconv.ParseInt(strings.TrimSpace(string(ct)), 10, 64); err == nil {
					since = sec*1000 - 1
				}
			}
		}
	}
	var turns []string
	seen := map[string]bool{}
	for start := 0; start < len(paths); start += 500 {
		chunk := paths[start:min(start+500, len(paths))]
		args := []any{since}
		marks := make([]string, len(chunk))
		for i, p := range chunk {
			marks[i] = "?"
			args = append(args, p)
		}
		rows, err := e.DB.Query(`SELECT o.turn_id,MIN(o.at) FROM ops o WHERE o.turn_id IS NOT NULL AND o.turn_id!='' AND o.at>?
			AND o.path IN (`+strings.Join(marks, ",")+`) GROUP BY o.turn_id ORDER BY MIN(o.at),o.turn_id`, args...)
		if err != nil {
			return nil, fmt.Errorf("finding turns: %w", err)
		}
		for rows.Next() {
			var id string
			var at int64
			if err := rows.Scan(&id, &at); err != nil {
				rows.Close()
				return nil, err
			}
			if !seen[id] {
				seen[id] = true
				turns = append(turns, id)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	return turns, nil
}
