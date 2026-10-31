# Trace

Trace keeps local keyframes of coding-agent turns so a project can be inspected, rewound, and forked.

Cline CLI has preview hooks: `trace setup --agent cline` enables global hooks, while `trace init --agent cline` or `trace agent add cline` installs project hooks. Existing hook scripts are preserved. Live Cline lifecycle verification remains outstanding.

Cursor has preview user-level hooks: run `trace setup --agent cursor` once to capture single-folder local workspaces without initializing each repository. Multi-root workspaces and cloud agents need separate handling; live verification remains outstanding.

Windsurf Cascade has preview user-level hooks: `trace setup --agent windsurf` installs hooks for the desktop IDE and JetBrains plugin. Restart the editor and confirm hooks are active; live verification and multi-repository behavior remain outstanding.

Copilot CLI has preview user-level hooks: `trace setup --agent copilot` enables configuration across repositories, respecting `COPILOT_HOME`. Restart Copilot and confirm hooks are active. Session-end events close open turns after a file scan. Project installs use `.github/hooks/usetrace.json`; refresh older installations with `trace agent add copilot`.

Gemini CLI has preview user-level hooks: `trace setup --agent gemini` installs hooks across repositories without a separate `trace init`. Global setup and project installation report that some Gemini versions require `hooks.enabled: true` in settings; enabling it can also activate other configured hooks. Gemini 0.25.2 recorded an isolated turn when hooks were enabled and the model API failed; successful live model turns remain unverified.

Qwen Code has preview user-level hooks: `trace setup --agent qwen` enables recording across repositories. It records submitted prompts when Qwen supplies them and closes failed turns; live verification remains outstanding.

Factory Droid has preview user-level hooks: `trace setup --agent factory` enables recording across repositories and captures cancelled turns. Live verification remains outstanding.

Claude Code project hook installation preserves existing hook fields, and removal merges against the current settings so later user changes are retained. Global setup respects `CLAUDE_CONFIG_DIR`; Codex setup respects `CODEX_HOME`. Codex stop hooks request a file scan, and the daemon favors observed hook events over corresponding legacy transcript events for the same session, with ordering limitations described in the CLI reference.

Multi-event hook callbacks are delivered to a compatible daemon as a single ordered, redacted line. Hooks retain the older individual-event format while an older daemon is running.

When the shared spool is locked, hooks publish redacted overflow files without waiting for that lock; a current daemon ingests them. Restart an older daemon after upgrading so it can process overflow files.

OpenCode has preview machine-wide plugins for v1 and v2: `trace setup --agent opencode` installs them for local Git repositories. Restart OpenCode and confirm the relevant plugin loads. The v2 observer captures available assistant text and supported tool activity; the v1 observer captures prompts and supported tool activity. The older per-repository OpenCode database poller is separate, opens the database read-only, and may duplicate v2 events if enabled. It retries while the database schema is being created and tolerates updates arriving up to one minute out of timestamp order. Live verification remains outstanding.

Kilo has a preview machine-wide plugin: `trace setup --agent kilo` installs it for local Git repositories; restart Kilo and confirm it loads. The plugin captures available assistant response parts and supported tool activity; live Kilo session behavior remains unverified.

Pi and Oh My Pi have preview machine-wide extensions: `trace setup --agent pi,omp` installs observers for new local Git repositories without per-repository setup. Restart the agents and confirm the extensions load; Pi and Oh My Pi turn capture was checked with offline providers; live workload behavior remains unverified. Set `OMP_PROFILE` when running setup to install into a named Oh My Pi profile. Pi and Oh My Pi project extensions and the Kilo project plugin use bounded, nonblocking subprocess delivery and request an edit scan at turn end.

Amp has a preview machine-wide plugin: `trace setup --agent amp` enables capture in local workspaces without per-repository setup. Amp's project plugin takes precedence when both are present; both capture available assistant text and modified tool paths, and request a scan at turn end. Live Amp verification remains outstanding.

Kimi Code CLI has preview global hook support: run `trace setup --agent kimi` once. This respects an absolute `KIMI_CODE_HOME`, preserves edited hooks, captures paths from Kimi's read, write and edit tools, and requests a scan when a turn stops or a session ends. Identified subagent lifecycle events do not close the parent turn. An offline Kimi 0.26.0 test recorded a prompt and closed its turn after interruption; successful model turns remain unverified.

Auggie has preview global hook support: run `trace setup --agent auggie` once. Trace routes using the workspace in its hook payload, including when Auggie starts from another directory. Sessions spanning multiple Git worktrees are skipped because their events cannot be attributed reliably. Asynchronous lifecycle hooks may arrive out of order, so turn boundaries and pre-edit snapshots are not guaranteed; live verification remains outstanding. Cline's preview hooks use available initial task text as a prompt fallback, capture multi-file read paths, and scan after failed mutating tools.

OpenHands CLI has preview global hook support: run `trace setup --agent openhands` once, with `OH_PERSISTENCE_DIR` set if OpenHands uses a custom persistence directory. Trace records file and planning editor operations. Existing OpenHands project hooks take precedence; merge Trace into those projects with `trace init --agent openhands` or `trace agent add openhands`. Hosted sessions and live turn capture remain unverified.

OpenClaude has preview global hooks that route events using the hook payload's working directory and scan when a turn fails or ends. Subagent lifecycle events do not close the parent turn. Refresh existing hooks with `trace setup --agent openclaude` or `trace agent add openclaude`. Live CLI capture remains unverified.

Claude Code hooks also scan and close open turns on API failure or session end, without treating subagent lifecycle events as the parent turn's end. Refresh existing hooks with `trace setup --agent claude-code` or `trace agent add claude-code`.

Gemini preview hooks scan for unreported edits at turn and session end, and project-hook detection handles Gemini's hook enablement settings. Gemini's session-end hook is best effort, and live lifecycle ordering remains unverified.

Qwen preview hooks honor `QWEN_HOME` during global setup and scan for unreported edits when a turn fails or ends, or when a session ends. Installed Qwen 0.0.14 did not emit the required hooks in an isolated test; use a hook-capable version. Live lifecycle ordering on newer versions remains unverified.

Qwen and Gemini project hooks route events using the session working directory, including when the hook process runs elsewhere.

Copilot preview global hooks use an absolute `COPILOT_HOME` when set and yield to matching Trace project hooks to avoid duplicate events. Subagent lifecycle events do not close the parent turn. If Copilot does not admit the project hooks, those events can be missed; live hook admission remains unverified.

Cursor project and preview global hooks route single-folder workspaces from the event payload, return Cursor's explicit allow response for read hooks, capture available subagent summaries and request a scan when the parent turn ends. Refresh existing hooks with `trace setup --agent cursor` or `trace agent add cursor`. Cursor's session-end callback is fire-and-forget, and live lifecycle delivery remains unverified.

The hook performance check covers global Cursor read and Gemini completion callbacks as well as the existing Antigravity path.

Hook events redact recognized secrets in text and metadata before spooling; sensitive session identifiers are hashed so events still correlate. Redaction is pattern-based.

Spool frames are retained if event processing fails, so a later daemon restart can retry them. Failed socket events and any remaining batch events are also spooled when possible. Replaying a partially processed spool frame can repeat events; a crash before socket events are processed or spooled can still lose them.

Factory Droid preview hooks route events using `FACTORY_PROJECT_DIR` when available, capture available subagent text and close open turns at session end. Refresh existing hooks with `trace setup --agent factory` or `trace agent add factory`; live lifecycle delivery remains unverified.

Auggie preview hooks resolve relative paths against the workspace, scan ambiguous multi-folder edits and unreported edits when a turn or session ends, and record optional response text when Auggie supplies it. Hook ordering and live turn capture remain unverified. The Aider Python wrapper records returned response text and prompt-bearing chat-mode commands; refresh it with `trace agent add aider`.

Poolside `pool` has preview global hooks for CLI 1.0.16 and later: `trace setup --agent poolside`. Setup reports the version requirement, merges ordinary block-style hook lists, and preserves other settings; complex YAML requires the manual steps in the CLI guide. Stop requests a scan for changes. Cancelled turns and stop-hook continuations have lifecycle gaps, and live turn verification is outstanding.

Hermes has a preview profile-scoped observer: `trace setup --agent hermes` installs it, then enable `trace-observer` in Hermes and set `TRACE_HERMES_REPO` to the repository to capture. Successful response text is recorded; failed, incomplete and interrupted turns request a scan and close. Run setup again to refresh an unchanged observer. Live turn capture and delivery at process exit remain unverified.

Zoo Code has a preview VS Code observer: `trace setup --agent zoo` generates a VSIX to install in the editor. It records task execution and follow-ups to completion requests; see the CLI reference for setup and limitations.

The shared JSON hook installer preserves unrelated settings and removes only Trace hook entries. It does not copy agent settings into Trace backup files.

LangGraph, CrewAI, Aider scripting, and Codebuff SDK support is available as preview bridges. The Aider and Codebuff bridges offer an explicit flush before script exit; see the CLI reference for registration and limitations.

Continue, Mistral Vibe, Crush, and Aider have preview single-prompt capture with `trace run <agent> --prompt "..."`. The Aider launcher disables its automatic commits. Interactive sessions are not yet captured by these launchers.

Run `trace setup` once to install automatic recording hooks for Claude Code and Codex in Git repositories. Their hook events use the reported session directory to select the worktree; Codex interruptions also close the current recorded turn. For other global integrations, choose an agent with `trace setup --agent NAME`; several are previews and may require a restart or other activation. Codex requires reviewing the installed hooks in `/hooks`. Start an agent in a repo and Trace creates its local history automatically. Use `trace status --json` to see the repository name and path. `trace setup --remove` removes the default global hooks. Per-repository setup is also available through `trace init` or `trace agent add`.

## Status

Optional Brain and Graph companions now refresh in the daemon after completed turns and sessions. `trace context --path main.go "authentication" --json` combines history, recalled memories and code relationships; MCP `get_context` returns the same result. `trace companions status` reports refresh status. Recovery rebuilds derived indexes from recovered history and the current checkout; manual memories remain local. Companion state is excluded from new snapshots. See [companion integration](docs/companions.md) for installation, protocol, controls and consistency limits.

Local chunk reads now close synchronously, including partial reads and errors. Sync protocol 2 transfers large files as bounded chunks in Team and Pro modes; upgrade the companion platform alongside the CLI. See [sync upgrade notes](docs/sync.md#protocol-2-upgrade) for repository identity changes and legacy hosted history.

The CLI records Claude Code, Codex, Cursor Agent, Antigravity CLI (`agy`), OpenCode, Windsurf Cascade, Gemini CLI, Copilot CLI, Qwen Code, Factory Droid, Pi, Oh My Pi, Grok Build, Goose, Amp, Kilo, and Kiro turns, stores compressed keyframes, and supports log, show, rewind, status, why, fork, resume, garbage collection, read-only MCP history tools, and Agent Trace export. The daemon stays resident until stopped, recovers automatically from the next agent session, and rotates its log. Rewind handles files changing into directories and back, removes empty directories created by earlier restores, preserves ignored files, and records the restored tree as the latest keyframe. It captures available assistant text, tool names, and read paths from agent hooks or transcripts. Optional Tree-sitter builds classify structural edits. An opt-in [Pro/Team sync client](docs/sync.md) uploads history in Worker-sized batches, appends corrections for completed turns and sessions, and can recover it into a fresh Git checkout through the separate Trace app. `trace login --auto-sync` opts a repository into background upload of existing and new history; without it, sync stays local until `trace sync` is run manually. `trace sync disable-auto` turns background upload off. `make test-sync` checks the client. Recent additions: `trace diff <a> <b>` compares any two keyframes, `trace messages` lists the conversation per turn, `trace worktree gc` cleans up Trace-created worktrees, `trace init --if-needed` is safe for scripts/agents, ops record per-edit actor attribution, and `trace doctor` self-diagnoses setup issues. The local CLI also offers keyword search across turns and messages with edited-path filters, recent-work dispatch, local activity reports, individual agent management, a timeline TUI, and diagnostics. See [quickstart](docs/quickstart.md), [CLI reference](docs/cli.md), and [privacy](docs/privacy.md).

## Install

macOS or Linux, with Git installed:

```sh
brew install UseTrace-SI/tap/trace
```

Or install the latest release archive with checksum verification:

```sh
curl -fsSL https://raw.githubusercontent.com/UseTrace-SI/trace-cli/main/scripts/install.sh | sh
```

Both install `trace` and its `trc` alias. The script defaults to `$HOME/.local/bin`; set `TRACE_INSTALL_DIR` to change it. To build from source instead, use Go 1.23 or newer:

```sh
git clone https://github.com/UseTrace-SI/trace-cli.git
cd trace-cli
make build
export PATH="$PWD/bin:$PATH"
```

Then, in any Git repository:

```sh
trace init --agent codex
```

Choose `claude-code`, `cursor`, `agy`, `opencode`, `windsurf`, `gemini`, `copilot`, `qwen`, `factory`, `pi`, `omp`, `grok`, `goose`, `amp`, `kilo`, or `kiro` instead of `codex` to install that agent's hooks. Goose project hooks capture available response text and scan for edits when a turn or session ends; live Goose verification and global setup are still pending. Factory Droid also has preview global setup with `trace setup --agent factory`, which routes sessions from their working directory without per-repository setup. After an agent turn, run `trace log`, `trace show 1`, or `trace why path/to/file.go:12`. Preview a restore with `trace rewind 1 --dry-run`. Fallback spool frames are retained until processing callbacks finish; an interruption during replay may repeat events. Damaged or oversized spool entries do not stop later events from being processed. See the [quickstart](docs/quickstart.md) for fork and resume.

The preview Aider Python bridge accepts both positional messages and Aider's `with_message` keyword. Interactive sessions and live model turns remain unverified.

Kilo project plugin installation and removal preserve user-modified plugin files. Live plugin loading and turn capture remain unverified.

Cline CLI project and global hook management preserves user-modified and symlinked hooks, captures available completion text, and requests a scan for unreported changes at task end. Global capture requires a single workspace root and yields only to unchanged project hooks for that repository. Live Cline lifecycle and editor support remain unverified.

OpenHands preview hooks route events using the SDK's working directory and request a scan when a turn or session ends. Live OpenHands capture remains unverified.

Newly completed turns include available hook-supplied assistant context in `trace messages`, including Antigravity prompts recovered at turn end; earlier turns are not backfilled. Sync now transfers message rows as well as turn summaries.

The preview `trace run vibe --prompt ...` launcher records bounded, redacted CLI output when neither output stream is a terminal or other character device. Interactive Vibe capture remains unimplemented.

Kiro hooks recognize its native file and shell tools, validate session and event identity, use the hook's workspace directory, and preserve edited hook files. Refreshed global hooks yield to an unchanged Trace project hook for the same event; run `trace setup --agent kiro` again to refresh older global installs. Live lifecycle and overlap verification remain outstanding.

## Documentation

| Guide | Covers |
| --- | --- |
| [Quickstart](docs/quickstart.md) | Record, inspect, rewind, and fork |
| [CLI reference](docs/cli.md) | Commands and flags |
| [How it works](docs/how-it-works.md) | Keyframes, daemon, adapters, and restore |
| [Privacy](docs/privacy.md) | Redaction, exclusions, and local storage |
| [Optional sync](docs/sync.md) | Encryption, recovery, and server protocol |
| [Release](docs/release.md) | Maintainer checks and packaging |

## Repository layout

| Path | Purpose |
| --- | --- |
| `cmd/trace`, `internal/` | Go CLI, daemon, and local storage |
| `docs/` | CLI, sync, privacy, and release guides |
| `scripts/` | Install, end-to-end, performance, and Homebrew tooling |

The Go module and project Makefile live at the repository root. Run `make lint test` for the CLI and `make test-sync` for its sync client. When the private `trace-platform` repository is checked out beside this one, `make test-sync-integration` exercises both sides of the protocol.

Trace is licensed under [Apache 2.0](LICENSE).
