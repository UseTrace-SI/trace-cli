# Quickstart

For automatic recording in new Git repositories, run `trace setup` once for Claude Code and Codex. Review and trust the Codex hooks using `/hooks`. Start the agent from a repository; Trace starts its local daemon and creates `.trace/` automatically. Run `trace status --json` to get the repo name and path. Use `trace setup --agent codex` to select one agent, or `trace setup --remove` to remove the global hooks. For OpenClaude or Kiro CLI V3, use `trace setup --agent openclaude` or `trace setup --agent kiro` (preview). Its default configuration is `~/.openclaude/settings.json`, or the directory specified by `OPENCLAUDE_CONFIG_DIR`. Other adapters use the per-repository commands below. The first baseline is created asynchronously, so edits made immediately after the session starts may precede it.

Factory Droid has preview global setup via `trace setup --agent factory`; remove it with `trace setup --agent factory --remove`. If Droid has a legacy `.factory/hooks/hooks.json`, migrate it using Droid's hooks manager before setup. Live hook delivery has not yet been verified.

Build Trace using the [source instructions](../README.md#build-and-try-it), put the `bin` directory on your `PATH`, and enter the Git repository whose work you want to record.

## Start recording

```sh
trace init --agent codex
trace status
```

`trace init` defaults to Claude Code. Choose `codex`, `cursor`, `agy`, `opencode`, `windsurf`, `gemini`, `copilot`, `qwen`, `factory`, `pi`, `omp`, `grok`, `goose`, `amp`, `kilo`, or `kiro` for another adapter; repeat `trace init` to enable more than one. `cursor` refers to Cursor Agent CLI and `agy` to Antigravity CLI.

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

To send history to the Trace workspace, run `trace login --auto-sync` and follow the GitHub device flow. Existing and new turns upload in the background; run `trace sync disable-auto` to stop it. See [sync](sync.md) for Pro encryption and recovery details.

See the [CLI reference](cli.md) for all commands and [privacy](privacy.md) for local storage and upload behavior.
