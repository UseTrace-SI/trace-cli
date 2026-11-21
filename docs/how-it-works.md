# How Trace works

## Capture and storage

Agent hooks send one redacted JSON line to a local Unix socket; a line can contain an ordered batch of events from one callback. If the socket is unavailable, the line is appended to `.trace/spool.jsonl`. If the spool lock is busy, the hook publishes a redacted file in `.trace/spool-pending/` without waiting for the lock. The daemon replays both locations; when Unix socket binding is denied, it polls them directly. Older daemons do not read pending files, so restart them after upgrading. The socket is mode `0600`; the daemon logs only event metadata and errors.

## Keyframes

Each completed turn gets a keyframe: a manifest mapping repository paths to content hashes. Blob bytes are compressed with zstd and stored under `.trace/objects/`. Trace ignores Git metadata, its own data directory, configured secret paths, and user integration files. File changes are also recorded as line-range operations for `why`. If a tool reports an edit without paths, the daemon scans the repository at turn end even if the watcher observed other changes. These scans can take longer in large repositories; if a file cannot be read, the turn keyframe is not written and the error is logged in `.trace/daemon.log`.

## Attribution across Git rewrites

`trace init` merges a `post-commit` and a `post-rewrite` block into the repository's Git hooks. They read only files in the Git directory (the reflog entry for the new commit, the rebase todo command being executed and `CHERRY_PICK_HEAD`) and the rewritten-commit pairs Git gives `post-rewrite`, and send them to the daemon. Nothing is written to Git: no notes, trailers or branches.

For each commit it observes, the daemon stores in the commit row: the commit's stable patch ID, the commits it rewrote (from an amend, rebase, interactive squash or fixup, cherry-pick, a `git merge --squash` commit that keeps Git's default message, or an earlier observed commit with the same patch ID), and the origin of every line the commit introduced. A line is explained through the commits it was copied from first, then through the edits Trace recorded before the commit, then, only for lines a squash brought in without naming its sources, by exact matches in other observed commits that are part of a run of at least three lines or long and unique. Post-rewrite pairs are also kept locally in `.trace/`.

`trace why` and `trace blame` follow a committed line with `git blame` to the commit that last changed it and use that commit's stored attribution, recomputing it from Git when needed. Uncommitted lines use the recorded edits alone. When recorded history shows a file returning to an exact earlier state, as a checkout, rebase or rewind does, the earlier state explains it. Lines that differ only in formatting (whitespace, indentation, quote style, semicolons, trailing commas or line wrapping) still match, with `normalized` confidence; a line that gained or lost any token does not. Lines that match nothing are reported as uncertain and never inherit an agent.

Commit rows travel with exported bundles, database imports and sync, so another checkout of the repository gives the same answers after importing history, even when the rewritten commits exist only in the first checkout. Post-rewrite pairs travel with bundles and database imports only.

## Agent context

For Claude Code and Antigravity CLI, the daemon reads newly appended assistant records from the session transcript at turn end. Claude Code's Stop hook also supplies the final assistant message when the transcript has not caught up yet. Trace stores redacted thinking or response text, tool names and safe repository paths, and read paths. A saved transcript offset keeps earlier turns out of later keyframes. Codex records assistant output and available reasoning summaries from rollout events. Cursor records thinking, responses, and read paths when its Agent CLI emits those hooks. Windsurf Cascade uses workspace hooks to record prompts, successful reads and writes, shell actions, and its response summary; the response closes the Trace turn. Gemini CLI hooks capture prompts, tool edits, and the final response before closing each turn. Copilot CLI hooks capture prompts, edits, reads, shell actions, and turn boundaries; its hook payload does not include the final assistant response. Qwen Code hooks capture submitted prompts, tool actions, and the final assistant message. Factory Droid hooks capture prompts, file and shell tools, and turn boundaries. The Pi and Oh My Pi extensions forward session, prompt, successful tool, and completion events through Trace’s redacting hook path.

## Restore and integrations

Rewind scans the current tree, saves a safety keyframe, then restores the requested manifest using temporary files and atomic renames, after checking that every object it needs is stored. The safety step can restore the state that existed before the rewind. The scan skips ignored and `.traceignore`d files, symlinks and other non-regular files, so a rewind that would replace one of them is refused before anything is saved or written; move the path aside or limit the restore with `--files`.

`trace mcp` exposes read-only history tools over standard input/output. It is registered in `.mcp.json` by `trace init` without replacing unrelated server entries.

The optional `treesitter` build tag uses Tree-sitter grammars for Go, TypeScript/TSX, Python, and Rust. It records function additions, removals, renames, and changes as structural operations with symbol names. The default binary remains pure Go.
