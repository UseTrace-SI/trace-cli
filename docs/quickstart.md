# Quickstart

Build Trace using the [source instructions](../README.md#build-and-try-it), put the `bin` directory on your `PATH`, and enter the Git repository whose work you want to record.

## Start recording

```sh
trace init --agent codex
trace status
```

`trace init` defaults to Claude Code. Choose `codex`, `cursor`, or `agy` for another adapter; repeat `trace init` to enable more than one. `cursor` refers to Cursor Agent CLI and `agy` to Antigravity CLI.

Initialization creates `.trace/`, adds it to Git's local `info/exclude`, installs the integration, and starts the daemon. Work with your coding agent normally; completed turns become keyframes.

## Inspect a turn

```sh
trace log
trace show 1
trace why path/to/file.go:12
```

`log` lists numbered steps. `show` displays saved context and a file diff. `why` finds the recorded edit behind a line. The amount of context depends on what the agent makes available to Trace.

## Rewind or fork

```sh
trace rewind 2 --dry-run
trace rewind 2
```

Preview first. A real rewind saves a safety keyframe and prints the command to undo the restore.

```sh
trace fork 2 --agent codex --no-launch
trace resume 2 --agent codex --no-launch
```

`fork` creates a separate Git worktree at that step. `resume` rewinds the current checkout and writes `.trace/context.md`; omit `--no-launch` to start the selected agent.

See the [CLI reference](cli.md) for all commands, [privacy](privacy.md) for local storage, and [sync](sync.md) for optional remote history.
