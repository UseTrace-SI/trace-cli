# How Trace works

## Capture and storage

Agent hooks send one redacted JSON line to a local Unix socket; a line can contain an ordered batch of events from one callback. If the socket is unavailable, the line is appended to `.trace/spool.jsonl`. If the spool lock is busy, the hook publishes a redacted file in `.trace/spool-pending/` without waiting for the lock. The daemon replays both locations; when Unix socket binding is denied, it polls them directly. Older daemons do not read pending files, so restart them after upgrading. The socket is mode `0600`; the daemon logs only event metadata and errors.

## Keyframes

Each completed turn gets a keyframe: a manifest mapping repository paths to content hashes. Blob bytes are compressed with zstd and stored under `.trace/objects/`. Trace ignores Git metadata, its own data directory, configured secret paths, and user integration files. File changes are also recorded as line-range operations for `why`. If a tool reports an edit without paths, the daemon scans the repository at turn end even if the watcher observed other changes. These scans can take longer in large repositories; if a file cannot be read, the turn keyframe is not written and the error is logged in `.trace/daemon.log`.

## Agent context

For Claude Code and Antigravity CLI, the daemon reads newly appended assistant records from the session transcript at turn end. Claude Code's Stop hook also supplies the final assistant message when the transcript has not caught up yet. Trace stores redacted thinking or response text, tool names and safe repository paths, and read paths. A saved transcript offset keeps earlier turns out of later keyframes. Codex records assistant output and available reasoning summaries from rollout events. Cursor records thinking, responses, and read paths when its Agent CLI emits those hooks. Windsurf Cascade uses workspace hooks to record prompts, successful reads and writes, shell actions, and its response summary; the response closes the Trace turn. Gemini CLI hooks capture prompts, tool edits, and the final response before closing each turn. Copilot CLI hooks capture prompts, edits, reads, shell actions, and turn boundaries; its hook payload does not include the final assistant response. Qwen Code hooks capture submitted prompts, tool actions, and the final assistant message. Factory Droid hooks capture prompts, file and shell tools, and turn boundaries. The Pi and Oh My Pi extensions forward session, prompt, successful tool, and completion events through Trace’s redacting hook path.

## Restore and integrations

Rewind scans the current tree, saves a safety keyframe, then restores the requested manifest using temporary files and atomic renames. The safety step can restore the state that existed before the rewind.

`trace mcp` exposes read-only history tools over standard input/output. It is registered in `.mcp.json` by `trace init` without replacing unrelated server entries.

The optional `treesitter` build tag uses Tree-sitter grammars for Go, TypeScript/TSX, Python, and Rust. It records function additions, removals, renames, and changes as structural operations with symbol names. The default binary remains pure Go.
