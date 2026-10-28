# How Trace works

## Capture and storage

Agent hooks send one redacted JSON event to a local Unix socket. If the socket is unavailable, the event is appended to `.trace/spool.jsonl` and replayed by the daemon. When Unix socket binding is denied, the daemon polls that spool directly. The socket is mode `0600`; the daemon logs only event metadata and errors.

## Keyframes

Each completed turn gets a keyframe: a manifest mapping repository paths to content hashes. Blob bytes are compressed with zstd and stored under `.trace/objects/`. Trace ignores Git metadata, its own data directory, configured secret paths, and user integration files. File changes are also recorded as line-range operations for `why`.

## Agent context

For Claude Code and Antigravity CLI, the daemon reads newly appended assistant records from the session transcript at turn end. Claude Code's Stop hook also supplies the final assistant message when the transcript has not caught up yet. Trace stores redacted thinking or response text, tool names and safe repository paths, and read paths. A saved transcript offset keeps earlier turns out of later keyframes. Codex records assistant output and available reasoning summaries from rollout events. Cursor records thinking, responses, and read paths when its Agent CLI emits those hooks.

## Restore and integrations

Rewind scans the current tree, saves a safety keyframe, then restores the requested manifest using temporary files and atomic renames. The safety step can restore the state that existed before the rewind.

`trace mcp` exposes read-only history tools over standard input/output. It is registered in `.mcp.json` by `trace init` without replacing unrelated server entries.

The optional `treesitter` build tag uses Tree-sitter grammars for Go, TypeScript/TSX, Python, and Rust. It records function additions, removals, renames, and changes as structural operations with symbol names. The default binary remains pure Go.
