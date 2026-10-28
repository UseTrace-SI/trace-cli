# Quickstart

Build Trace using the [source instructions](../README.md#build-and-try-it), enter a Git repository, and run `trace init`. By default, Trace installs Claude Code hooks. Run `trace init --agent codex`, `--agent cursor`, or `--agent agy` for those agents. Multiple adapters can be enabled in the same repository. `agy` is Google Antigravity CLI. Cursor Agent CLI is available as `agent` or `cursor agent` even when the Cursor IDE is absent.

Trace stores its database, snapshots, logs, and context files under `.trace/`. Initialization adds that directory to Git's local `info/exclude` file.

After an agent turn, inspect history with `trace log`, `trace show 1`, or `trace why path/to/file.go:12`. Use `trace rewind 2 --dry-run` to preview a restore. A real rewind saves a safety step and prints the command to undo it.

Use `trace fork 2 --no-launch` to create a detached Git worktree at a saved step. Use `trace resume 2 --agent codex --no-launch` to rewind the current tree and write `.trace/context.md` for a different agent.
