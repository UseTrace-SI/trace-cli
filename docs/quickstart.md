# Quickstart

## Install once, log in, enable

Install the coordinated bundle and put its binary directory on your `PATH`.
Then, from any directory:

```sh
trace login
trace enable
```

Enter the short code printed by `trace login` on the website it opens. Approval
connects this computer to your Trace account. `trace enable` detects supported installed CLI and desktop integrations, or resumes the saved selection. Complete the reported hook trust,
restart or extension activation steps, then work in Git projects normally.
History and derived context sync automatically into your private dashboard.
The supported install targets are macOS/Linux on Intel/ARM. Integrations that
are marked preview are still preview; desktop apps without an integration are
not captured. SDK bridges still need registration in the application.

```sh
trace agents --json          # discover integrations for automation
trace enable --all           # enable supported agents detected here
trace enable --agent codex   # choose one agent, remembered across projects
trace disable --agent codex  # pause recording for one agent
trace disable               # pause recording and automatic uploads
trace enable                # resume, preserving login and history
trace sync                  # optional immediate upload in a project
trace sync status           # inspect pending rows and last upload
trace sync disable-auto     # keep this project local
trace sync enable-auto      # resume automatic uploads for this project
```

Use `trace login --no-browser` on a remote machine and enter its code in a browser
on another device. Sign in separately for each OS user/computer. For local-only
recording, skip login and run `trace enable`. Existing project-only connections
and per-project pauses take precedence over the global login.

The commands below remain available for explicit per-project setup.

For automatic recording in new Git repositories, run `trace setup` once for Claude Code and Codex. Review and trust the Codex hooks using `/hooks`. Start the agent from a repository; Trace starts its local daemon and creates `.trace/` automatically. Run `trace status --json` to get the repo name and path. Use `trace setup --agent codex` to select one agent, or `trace setup --remove` to remove the global hooks. For OpenClaude or Kiro CLI V3, use `trace setup --agent openclaude` or `trace setup --agent kiro` (preview). Its default configuration is `~/.openclaude/settings.json`, or the directory specified by `OPENCLAUDE_CONFIG_DIR`. Other adapters use the per-repository commands below. The first baseline is created asynchronously, so edits made immediately after the session starts may precede it.

Factory Droid has preview global setup via `trace setup --agent factory`; remove it with `trace setup --agent factory --remove`. If Droid has a legacy `.factory/hooks/hooks.json`, migrate it using Droid's hooks manager before setup. Live hook delivery has not yet been verified.

Install the coordinated bundle using the [installation instructions](../README.md#install), put its installation directory on your `PATH`, and enter the Git repository whose work you want to record.

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

## Connect your hosted dashboard

Install the coordinated CLI, Brain and Graph bundle from usetrace.si. Sign in
to its hosted dashboard, then run this in your initialized repository:

```sh
trace connect --remote you/project
```

Approve the device under the account that should own its history. The project
name can match a GitHub repository or use your own `you/project` label. GitHub
access is not required. Existing history and future turns upload automatically.
Refresh the hosted timeline to see new sessions. The project context view shows
the latest derived memories and code index with capture/index times. Manual notes
stay on this machine.

For a remote terminal, use `trace connect --no-browser` and open the approval URL
on your signed-in device. `trace sync` uploads immediately; `trace sync disable-auto`
stops background uploads. Disconnect a CLI in the dashboard to revoke its access.
Connections expire after 90 days; reconnect to renew.

Personal history is encrypted by the server so it can be displayed after login.
Legacy client-encrypted Pro sync remains available with `trace login --mode pro`;
its payloads remain opaque to the dashboard. See [sync](sync.md) for those modes and recovery.


See the [CLI reference](cli.md) for all commands and [privacy](privacy.md) for local storage and upload behavior.
