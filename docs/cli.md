# CLI reference

`trace login [--endpoint https://usetrace.si] [--device NAME] [--no-browser]` signs this computer in with a short code entered on the website. It works outside a repository. `trace enable [--agent NAME,...]` enables the supported user-level integrations across projects; without `--agent`, it resumes the saved selection or detects supported installed agents. `trace disable` pauses recording and automatic uploads across projects without removing the login or history. Agent trust/restart/extension steps remain required where reported.

`trace sync` uploads the current project immediately; normal uploads happen automatically after login and enablement. `trace sync disable-auto` and `enable-auto` control this project independently. `trace login --mode pro` and `--mode team` preserve legacy GitHub sync.

`trace connect [--remote you/project] [--endpoint https://usetrace.si] [--no-browser]`
approves a private connection to your hosted dashboard and enables background
upload. Run it from an initialized repository. Sign in and approve the displayed
device in your browser; an account alone does not authorize a CLI until approval.
Use the dashboard's Connected CLIs view to revoke it, or reconnect after expiry.
GitHub repository access is not required for this personal mode.

For Claude Code, `trace setup --agent claude-code` installs user hooks in `~/.claude/settings.json`, or `$CLAUDE_CONFIG_DIR/settings.json` when that absolute directory is set. Use the same profile setting when removing them. Claude Code 2.1.312 ran identical user and project SessionStart commands once in an isolated offline startup test, including with different matching groups; other lifecycle events and versions remain unverified.

For OpenCode, `trace setup --agent opencode` installs preview v1 and v2 observers in `~/.config/opencode/plugins/` (or the corresponding `$XDG_CONFIG_HOME/opencode` or `$OPENCODE_CONFIG_DIR` path). Restart the OpenCode server and confirm the relevant plugin loads; remove both with `trace setup --agent opencode --remove`. The v1 observer records non-synthetic prompt text, supported tools, and session idle; another prompt while the session is active is not recorded separately. It skips repositories with the Trace project database poller enabled. The v2 observer tracks admitted user inputs and starts a turn when OpenCode reports delivery. A later delivered prompt closes the preceding turn, and execution success, failure, or interruption closes an active turn; each close requests an edit scan. Cancelled or synthetic inputs do not start turns. The v2 observer captures completed assistant text and reasoning, successful read paths, and supported edit, write, shell, and patch tool names; raw tool arguments and results are not forwarded. Event delivery and hook forwarding are asynchronous, so snapshots can lag, delivery can be lost, and bounded in-memory tracking may omit inputs when its limits are reached. Remote servers, session moves and live turn capture remain unverified. The separate read-only project database poller may produce duplicate v2 events; remove that project integration with `trace agent remove opencode` before relying on the v2 observer.

For Kilo, `trace setup --agent kilo` installs a preview global plugin in `~/.config/kilo/plugin/trace-global.ts` (or `$XDG_CONFIG_HOME/kilo/plugin/trace-global.ts`). Restart Kilo and confirm it loads; remove it with `trace setup --agent kilo --remove`. Kilo loads plugins for local session directories, and Trace selects the containing Git worktree. When the current project plugin loads for the same worktree, it signals the global plugin to avoid duplicate events; a project plugin loaded from another worktree does not claim the session. Refresh existing installations with `trace setup --agent kilo` and `trace agent add kilo`: older project plugins do not signal the global plugin and can produce duplicates. `KILO_PURE` disables external plugins. The plugin records successful read paths and edit, write, shell, and patch tool names; raw tool arguments and outputs are not forwarded. It records available assistant text from message and part updates when Kilo identifies the assistant message before its text parts. It retains bounded in-memory text until session idle and sends it through Trace’s redaction path; response text may be incomplete if events are missing or reordered. Events are queued asynchronously and can be lost on queue overflow or exit, and a baseline may be late. Live CLI loading, overlapping sessions, and turn capture remain unverified.

For Pi and Oh My Pi, `trace setup --agent pi,omp` installs preview global extensions in `~/.pi/agent/extensions/trace-global/` and `~/.omp/agent/extensions/trace-global/`. Restart the agent and confirm the extension loads. Remove with `trace setup --agent pi,omp --remove`. `PI_CODING_AGENT_DIR` selects a custom absolute agent directory; when both agents use the same directory their installations conflict and setup protects the existing extension. Oh My Pi's `PI_CONFIG_DIR` selects its config root. To enable a named profile, run `OMP_PROFILE=work trace setup --agent omp` once for that profile (and use the same environment for removal); profiles do not inherit the default profile's extension. `OMP_PROFILE` takes precedence over the legacy `PI_PROFILE`, and named profiles ignore `PI_CODING_AGENT_DIR`. An Oh My Pi `--profile` argument on a later agent invocation cannot be detected during Trace setup. Extensions disabled by agent settings or `--no-extensions`, and sessions without a session file, are not recorded. When a current project Trace extension is loaded in the same working directory, the global extension skips events to avoid duplicate recording. Older project extensions need to be refreshed with `trace agent add pi` or `trace agent add omp`; otherwise both may record events. Both project and global extensions request an edit scan at turn end for custom tools. Their subprocess delivery is asynchronous and bounded: queue overflow or agent exit can lose events, and the baseline can be delayed. Re-run `trace agent add pi` or `trace agent add omp` to update an existing project extension. Pi 0.65.0 and Oh My Pi 18.1.18 recorded offline test turns; Pi was also checked with the project extension loaded, and Oh My Pi loaded the global extension from a named profile. Delivery under real workloads remains unverified.

For Amp, `trace setup --agent amp` installs a preview machine-wide plugin at `~/.config/amp/plugins/trace.ts` (or `$XDG_CONFIG_HOME/amp/plugins/trace.ts`). Remove it with `trace setup --agent amp --remove`. Reload plugins in Amp and check `amp plugins list`; activation settings can prevent it from running. Amp gives a project plugin with the same name precedence over the machine-wide one. The plugin only records local workspaces with an absolute path supplied by Amp; it does not record remote Orbs. Events are queued asynchronously and can be lost when the queue fills or Amp exits; overlapping threads can include each other's edits. Baseline timing and live turn capture remain unverified.

For Qwen Code, `trace setup --agent qwen` installs preview user-level hooks in `~/.qwen/settings.json`, or `$QWEN_HOME/settings.json` when set; remove them with `trace setup --agent qwen --remove` using the same environment. Use an absolute `QWEN_HOME` or `~/` path. If Qwen loads it from a home-scoped `.env` file, export it for Trace setup too. Existing settings are preserved. Qwen 0.0.14 did not emit the required hooks in an isolated failed-API test; use a newer hook-capable release. The session `cwd` selects the Git worktree; hooks from identified subagents are ignored. Only events containing `submitted_prompt` start turns, because Qwen can also send internal continuations through the prompt hook. Stop, failure and session end request a scan before closing. The global hook skips matching Trace project hooks; if Qwen does not load those hooks because the workspace is untrusted, the event will be missed. Live verification on newer Qwen remains outstanding.

For Gemini CLI, `trace setup --agent gemini` installs preview user-level hooks in `~/.gemini/settings.json` (or `$GEMINI_CLI_HOME/.gemini/settings.json` when set); remove them with `trace setup --agent gemini --remove`. Existing settings are preserved. Some versions, including 0.25.2, require `"enabled": true` inside the `"hooks"` object; this can also activate other configured hooks, so Trace does not change that setting. The session `cwd` selects the Git worktree. Where a matching Trace project hook exists, the global hook skips that event to prevent duplicates; if Gemini does not load the project hook because the workspace is untrusted, that event will be missed. An isolated Gemini 0.25.2 API-failure turn was recorded with hooks enabled; successful model turns remain unverified.

For Cursor, `trace setup --agent cursor` installs preview user-level hooks in `~/.cursor/hooks.json`; remove them with `trace setup --agent cursor --remove`. Existing hooks are preserved. Global hooks record local sessions with one workspace folder and ignore multi-root workspaces because a prompt does not identify a single owning repository. When the matching Trace project hook is installed in that workspace, the global hook skips that event to avoid duplicates. User-level hooks do not run in Cursor cloud agents. Cursor may also import Claude Code hooks; their interaction with Trace's Cursor hooks remains unverified. Live Cursor verification remains outstanding.

For Windsurf Cascade, `trace setup --agent windsurf` installs preview user hooks in `~/.codeium/windsurf/hooks.json` for the desktop IDE and `~/.codeium/hooks.json` for the JetBrains plugin. Remove both with `trace setup --agent windsurf --remove`. Existing hooks are preserved. The hook process's default working directory selects the Git worktree; a matching Trace project hook takes precedence for that event. Hooks do not run in restricted mode. Cascade's response hook is asynchronous, so event ordering, overlapping turns and multi-repository routing remain unverified; test live capture after restarting the editor. The separate Devin Local agent uses different hooks and is not covered by this integration.

For Copilot CLI, `trace setup --agent copilot` installs preview user hooks in `$COPILOT_HOME/hooks/usetrace.json`, or `~/.copilot/hooks/usetrace.json` when `COPILOT_HOME` is unset. Use an absolute `COPILOT_HOME` so the hook location does not depend on the launch directory. Restart Copilot to load changes; remove them with `trace setup --agent copilot --remove` using the same profile. For project setup, `trace agent add copilot` installs `.github/hooks/usetrace.json`; rerun it to add the supported hook file if an older Trace installation used an unsupported settings path. Existing hooks are preserved. On session end, Trace scans for edits and closes an open turn, including after an abort or timeout. The global hook yields to an unchanged Trace project hook for the same event; if Copilot does not admit that project hook, the event may be missed. Workspace admission, disabled hooks, sandbox restrictions, stop-hook continuations, and live turn capture remain unverified.

Copilot CLI also runs hooks from a repository's `.claude/settings.json` and `.claude/settings.local.json`, so Trace's Claude Code hooks fire in Copilot sessions too. Trace recognizes those calls by payload shape: GitHub documents a `sessionId` field (camelCase payloads) or a `timestamp` field (the snake_case payloads it sends for PascalCase event names), and Claude Code's own payloads carry neither. Such calls are recorded as `copilot` sessions, print nothing on stdout (Copilot treats empty output as the default behavior), and are dropped when Trace's own Copilot hook (project or `trace setup --agent copilot`) records the same event, or when Copilot is disabled with `trace disable --agent copilot`. `trace disable --agent claude-code` also stops these calls. Copilot's `StopFailure` equivalent is not recorded through this path.

For Codex, `trace setup --agent codex` installs user-level hooks in `$CODEX_HOME/hooks.json` when `CODEX_HOME` names an existing directory, or `~/.codex/hooks.json` otherwise. Relative paths resolve from the current working directory. Use the same profile for removal. Review and trust the hooks with `/hooks` in Codex; disabled hooks or managed-only policy can prevent them from running. Hook events use the session's reported working directory to select the Git worktree, and an `Interrupt` event closes the current recorded turn after requesting a file scan. The older project transcript poller also uses the `CODEX_HOME` inherited by its daemon; restart that daemon after switching profiles. While both sources run, the daemon favors hook events after observing that event kind for the same session and tool name, while retaining transcript context and fallback for kinds not yet observed. Context and events that reach the poller first can still overlap, and changing hook trust during a session may require a daemon restart for transcript fallback. This per-kind preference is kept in daemon memory for 24 hours after the session's last hook event, and is cleared by the session's end hook or a daemon restart; transcript events after that are recorded again. Events that arrive after a session ended, for example rollout lines read after Codex's session-end hook, continue the same recorded session instead of creating a second one. Rerun setup after upgrading Trace to add the interruption hook to an existing installation.

For Factory Droid, `trace setup --agent factory` installs preview user-level hooks in `~/.factory/hooks.json`, or merges into existing hooks in `~/.factory/settings.json` if there is no standalone file. Remove them with `trace setup --agent factory --remove`. The session working directory selects the Git worktree, and idle notifications close cancelled turns. Matching Trace project hooks take precedence to avoid duplicate events. If a legacy `.factory/hooks/hooks.json` exists, migrate it with Droid's hooks manager before installing Trace. Live turn capture and project/global overlap remain unverified. Rerun project setup to add the cancellation hook to existing installations.

For Cline CLI, `trace setup --agent cline` installs preview global hooks in `~/.cline/hooks/` (or `$CLINE_DIR/hooks/`). Remove them with `trace setup --agent cline --remove`. Alternatively, `trace init --agent cline` or `trace agent add cline` installs project hooks in `.cline/hooks/`. Trace refuses to replace existing scripts at those paths. Global hooks require a single workspace root and skip events handled by unchanged Trace project hooks for that repository. When task start includes the initial task text, Trace uses it as a prompt fallback only if the session has no recorded turn; later prompts still require the prompt hook. Relative tool paths use the reported workspace; ambiguous multi-workspace edits and failed mutating tools request a scan instead. Multi-file reads capture up to 256 reported paths and omit non-file URIs. This targets the current CLI/SDK lifecycle; VS Code and JetBrains support and live session verification remain unfinished. Cline CLI versions with the reported prompt-hook issue may miss later prompts. Cline's lifecycle hooks may run asynchronously, so ordering remains to be verified in live sessions.

For Kimi Code CLI, `trace setup --agent kimi` installs preview user-level hooks in `~/.kimi-code/config.toml` (or `$KIMI_CODE_HOME/config.toml`). Remove them with `trace setup --agent kimi --remove` using the same profile. If set, `KIMI_CODE_HOME` must be an absolute path, so it selects the same profile in every repository. Existing hook entries are preserved. The reported session working directory selects the Git worktree; events without a usable directory are ignored. Trace records paths from Kimi's read, write and edit tools. Identified subagent lifecycle events do not close the parent turn, while their file edits can be recorded. Another hook can block a prompt or stop after Trace records it, producing a misleading boundary. An isolated Kimi 0.26.0 run against an unresponsive offline provider recorded the prompt and closed its turn on session end after interruption. Successful model turns, normal completion events and native transcript import remain unverified.

For Auggie, `trace setup --agent auggie` installs preview user-level hooks in `~/.augment/settings.json`; remove them with `trace setup --agent auggie --remove`. Existing hooks are preserved. Trace resolves workspace roots from the event payload, falling back to the session working directory only when none are provided. Sessions spanning multiple Git worktrees and events without a usable repository are ignored. Reported file-change paths are captured without their contents; when multiple workspace folders make a relative edit path ambiguous, Trace requests a full scan. Prompt and stop hooks run asynchronously, so turn ordering and pre-edit snapshots are not guaranteed. Live agent verification and transcript import remain outstanding.

For OpenHands CLI, `trace setup --agent openhands` installs preview hooks in `~/.openhands/hooks.json`, or `$OH_PERSISTENCE_DIR/hooks.json` when set. Use an absolute path or `~/` for `OH_PERSISTENCE_DIR`; relative paths depend on where OpenHands starts and are rejected by Trace setup. Remove them with `trace setup --agent openhands --remove`. Trace records file-editor and planning-file-editor operations. OpenHands loads a project `.openhands/hooks.json` instead of the global file. In a repository with an existing project hooks file, run `trace init --agent openhands` or `trace agent add openhands` to merge Trace's hooks; remove them with `trace agent remove openhands`. Trace does not create a project hooks file if none exists, because that would shadow the user's global hooks. A different stop hook may request a retry after Trace observes a stop, producing an early boundary. Hosted sessions, live turn capture and transcript import are not verified.

For Poolside `pool` 1.0.16 and later, `trace setup --agent poolside` installs preview global hooks in `~/.config/poolside/settings.yaml`. It preserves existing settings and merges ordinary two-space block-style hook lists. Remove the managed entries with `trace setup --agent poolside --remove`. For flow-style or complex YAML, merge the following entries into the existing `hooks` mapping instead; ensure `trace` is on Pool's PATH, or use its absolute executable path:

```yaml
hooks:
  SessionStart:
    - name: trace-sessionstart
      matcher: "*"
      command: "trace hook poolside SessionStart"
  UserPromptSubmit:
    - name: trace-userpromptsubmit
      matcher: "*"
      command: "trace hook poolside UserPromptSubmit"
  PostToolUse:
    - name: trace-posttooluse
      matcher: "*"
      command: "trace hook poolside PostToolUse"
  Stop:
    - name: trace-stop
      matcher: "*"
      command: "trace hook poolside Stop"
```

Pool has no session-end hook and does not send Stop for a cancelled turn. Another stop hook can request continuation after Trace observes a stop. Live Poolside turn capture remains unverified.

For Hermes Agent, `trace setup --agent hermes` installs a preview observer in `$HERMES_HOME/plugins/trace-observer` (default `~/.hermes/plugins/trace-observer`). Enable it in the same profile with `hermes plugins enable trace-observer --no-allow-tool-override`, and set `TRACE_HERMES_REPO` to the absolute repository path before starting Hermes. This explicit path prevents gateway or remote sessions from being assigned to an unrelated repository. `trace setup --agent hermes --remove` removes the unmodified observer files; use Hermes to disable the plugin as well.

The observer queues events on a background thread to avoid delaying Hermes. Failed, incomplete and interrupted turns request a scan and close; successful turns capture available response text. Run `trace setup --agent hermes` again to upgrade an unchanged observer. The bounded queue can drop events when full, and pending events can be lost when Hermes exits. Text fields longer than 65,536 characters are omitted. Subagent starts are excluded when identified. Live turn capture remains unverified.

## Continue, Vibe, Crush, and Aider headless preview

Record an [Agent Client Protocol](acp.md) agent by launching `trace acp [--name NAME] [--stop-grace 5s] -- <agent command> [args...]` from the editor in place of the agent. The proxy forwards stdio unchanged and records sessions in initialized repositories as agent `acp:<name>`.

Run a single headless prompt with `trace run <agent> --prompt "..."` inside a Git repository, where agent is `continue`, `vibe`, `crush`, or `aider`. Trace takes a baseline before launching `cn -p`, `vibe -p`, `crush run`, or `aider --message`, records the end of the invocation, and preserves the agent's output and exit status. The Aider launcher passes `--no-auto-commits --no-dirty-commits` to prevent its default automatic commits. If recording cannot start, the agent still runs. These launchers accept no extra agent options and do not cover interactive sessions, native resume attribution, or assistant transcript import. A failed run is recorded as an ended execution, without a distinct failure status. Interrupting Trace itself may leave the execution unfinished.

## Zoo Code task observer preview

Run `trace setup --agent zoo`, then install the generated package into VS Code and reload the window:

```sh
code --install-extension "$HOME/.trace/integrations/trace-zoo.vsix"
```

The observer uses Zoo Code's extension API for task start, completion, and abort events. When Zoo presents a completion result, Trace ends the current turn; feedback to that completion starts a new turn. Other mid-task feedback is grouped into the current turn because the API does not identify all submitted prompts reliably. It only records tasks whose history item has an absolute workspace matching a local folder open in the editor, and skips known subtasks and tasks with missing history or workspace information. Task startup and event delivery are asynchronous, so a baseline can be late, a bounded queue can drop events, and events pending when the editor exits may be lost. Overlapping tasks in one workspace may include each other's changes. Zoo Code's API may omit events for some tabs; live editor behavior remains unverified. The original task text from the history item is used as the prompt, including when a task is resumed.

To remove it, run `trace setup --agent zoo --remove`, uninstall `usetrace-si.trace-zoo` from the editor, and reload the window. Removing the package does not itself uninstall the editor extension.

## Codebuff SDK preview

Run `trace init --agent codebuff` (or `trace agent add codebuff` in an initialized repository). Wrap an existing SDK client:

```js
import { traceCodebuff } from './.trace/integrations/trace_codebuff.mjs';

const traced = traceCodebuff(client);
const result = await traced.run({ agent: 'base', prompt: 'Describe the work' });
traced.finish();
await traced.flush(); // Optional: wait for queued events before exiting.
```

This covers explicit SDK `run` calls, not interactive Codebuff or Freebuff CLI sessions. Concurrent calls through the same wrapper are passed through, but only the first receives a turn; its snapshot may include edits from the other calls. `flush()` waits for events queued when it is called, with a two-second timeout per hook; it cannot recover events dropped by the bounded queue. Without it, pending events can be lost when the process exits. SDK live verification remains outstanding.

## Aider preview

Run `trace init --agent aider` (or `trace agent add aider` in an initialized repository). Add `.trace/integrations` to the Python module path and wrap explicit Aider `Coder.run(message)` calls:

```python
from trace_aider import TraceAider

traced = TraceAider(coder)
result = traced.run("Describe the work")
traced.finish()
traced.flush(timeout=5)  # Optional: wait for queued events before exiting.
```

This covers explicit Python calls only; interactive Aider CLI sessions are not captured. Successful calls record a bounded response when Aider returns text. Prompt-bearing `/ask`, `/code`, `/architect` and `/context` calls are captured; mode switches without a prompt and other commands are skipped. Re-run `trace agent add aider` to upgrade an unchanged bridge. Aider describes its Python scripting API as unsupported and subject to change. Overlapping calls pass through unchanged, but only the first receives a turn; its snapshot may include edits from the other calls. The background queue can drop events when full or when the process exits. `flush()` waits up to the requested timeout and returns whether the queue drained; live Aider verification remains outstanding.

## CrewAI preview

Run `trace init --agent crewai` (or `trace agent add crewai` in an initialized repository). Add `.trace/integrations` to the Python module path and create a listener before starting a crew:

```python
from trace_crewai import TraceCrewListener

trace_listener = TraceCrewListener()
result = crew.kickoff(inputs={"prompt": "Describe the work"})
```

This records each correlated crew kickoff as one execution, not each individual agent turn. It uses CrewAI's `started_event_id` to match completion or failure to the start; versions or events without that correlation cannot close the execution. The bounded background queue can drop events when full or at process exit. Live CrewAI verification remains outstanding.

## LangGraph preview

Run `trace init --agent langgraph` (or `trace agent add langgraph` in an initialized repository). Add `.trace/integrations` to the Python module path and register the callback on each root graph invocation:

```python
from trace_langgraph import TraceCallbackHandler

handler = TraceCallbackHandler()
result = graph.invoke(inputs, config={"callbacks": [handler]})
```

The callback records root graph starts and completions and ignores nested chains. It forwards events in a background thread, so a process that exits immediately after invocation can lose queued events. Transcript import and live framework verification remain unfinished.

Run `trace --help` or `trace <command> --help` for the installed version's full flag list. The global `--repo PATH` flag selects a repository; `--json` requests machine-readable output where a command supports it. Step arguments are the numbers shown by `trace log`.

## Commands

| Command | Purpose |
| --- | --- |
| `trace version` | Print build version, commit, and date |
| `trace setup [--agent NAME] [--remove]` | Enable or remove selected user-level agent integrations |
| `trace init [--agent NAME]` | Set up local history and an agent adapter |
| `trace uninstall [--purge]` | Remove integrations, optionally deleting history |
| `trace log` | List keyframes |
| `trace show STEP` | Show a turn and file diff |
| `trace rewind STEP [--files GLOB] [--dry-run]` | Restore a snapshot with an undo step |
| `trace why FILE:LINE [--all] [--messages]` | Find the change responsible for a line, across Git rewrites, and optionally show its conversation |
| `trace blame FILE` | Show the origin, commit, confidence and rewrites of every line |
| `trace fork STEP [--agent NAME] [--name SUFFIX] [--no-launch]` | Create a worktree at a step |
| `trace resume STEP\|SESSION [--agent NAME] [--fork [--name SUFFIX]] [--no-rewind] [--no-launch] [--step\|--session] [--max-context-bytes N]` | Continue a recorded session in the same or another agent; see [Cross-agent resume](#cross-agent-resume) |
| `trace review [--agent NAME\|--agents A,B] [--judge NAME] [--with-provenance] [--format text\|markdown\|sarif] [--staged] [--no-launch] [--timeout D]` | Review unstaged (or staged) changes with independent headless agents, optionally consolidated by a judge; see [independent review](#independent-review) |
| `trace status` | Show daemon, session, and storage status |
| `trace activity [--days N]` | Summarize local turns, sessions, changed files, and agent activity |
| `trace search [TERMS...] [--scope all\|turns\|messages] [--edited-path GLOB] [--limit N]` | Search completed turns by text and edited path |
| `trace messages [--step N] [--session ID] [--limit N] [--offset N]` | Read recorded conversation, optionally filtered and paged |
| `trace diff STEP STEP [--content]` | Compare two keyframes |
| `trace dispatch [--limit N]` | Print a chronological summary of recent local turns |
| `trace agent add NAME` | Enable one agent adapter in an initialized repository |
| `trace agent remove NAME` | Remove one agent adapter and its installed hooks |
| `trace agent-help` | Print commands available to coding agents |
| `trace doctor [--bundle FILE]` | Check setup, store size against `TRACE_MAX_STORE_BYTES` and recently dropped hook events; bundle contains check results, not daemon log contents |
| `trace clean [--force]` | Preview removal of unlinked local history; apply with `--force` |
| `trace tui` | Open the local timeline viewer |
| `trace dashboard [--port N]` | Serve a read-only web view on 127.0.0.1; open the printed URL, whose one-launch token is required for history and is set as an HttpOnly, SameSite=Lax cookie. Requests naming any other host are refused |
| `trace worktree gc` | Remove eligible Trace-created worktrees |
| `trace gc [--keep-days N] [--dry-run]` | Remove expired history and unused blobs. The daemon also runs it automatically with the default 30-day retention at most once a day (hourly while `.trace` exceeds `TRACE_MAX_STORE_BYTES`, default 5 GiB), never while sync is configured; `TRACE_AUTO_GC=0` turns that off |
| `trace update-check` | Check for a release, at most once per day |
| `trace config get\|set commit-trailer [on\|off]` | Opt in to (or out of) `Trace-Turn:` commit trailers; off by default |
| `trace export --format agent-trace [--rev COMMIT\|A..B] [--out FILE]` | Export line attribution as [Agent Trace](https://agent-trace.dev) JSONL (stdout without `--out`) |
| `trace export --format markdown --out DIR [--session ID]` | Write one redacted Markdown file per session: prompts, last response per turn, files touched |
| `trace import --from git-ai` | Read git-ai authorship notes (`refs/notes/ai`) read-only as attribution hints for Agent Trace export |
| `trace export --format bundle --out FILE [--include-excluded-history]` / `trace handoff [--out FILE] [--include-excluded-history]` | Write a portable history bundle. Earlier snapshots of since-excluded credential files are left out and listed unless the flag keeps them |
| `trace login --remote OWNER/REPO --mode pro\|team --endpoint HTTPS_URL [--auto-sync] [--device-auth]` | Sign in with GitHub in a browser; opt into background upload |
| `trace sync [--watch]` | Upload local history to the configured sync server |
| `trace sync disable-auto` | Turn off background sync for this repository |
| `trace sync pull --into PATH` | Recover synced history into a fresh Git checkout |
| `trace daemon start` | Start the local daemon if it is not running |
| `trace daemon stop` | Stop the local daemon |
| `trace mcp` | Serve read-only MCP tools over stdio |

Agent names for `init` and `agent add` include `claude-code`, `codex`, `cursor`, `agy`, `opencode`, `windsurf`, `gemini`, `copilot`, `qwen`, `factory`, `pi`, `omp`, `grok`, `goose`, `amp`, `openclaude`, `openhands`, `kilo`, and `kiro`. OpenHands project integration requires an existing `.openhands/hooks.json`. Re-run `trace init` or use `trace agent add` to enable another adapter in the same repository. `trace agent remove` removes only the named adapter; `trace uninstall` removes all integrations. Copilot project hooks use `.github/hooks/usetrace.json`; refresh older installations. Kilo project plugins request a full edit scan at session idle and use bounded asynchronous event delivery; queued events can be lost on exit or overflow. Refresh with `trace agent add kilo`. Kilo CLI plugin loading and turn capture remain unverified. Grok project hooks require folder trust. Amp uses a project plugin and may require plugin activation. Goose uses a project plugin. Pi and Oh My Pi use project TypeScript extensions under `.pi/extensions/trace/` and `.omp/extensions/trace/`. Factory Droid uses `.factory/hooks.json`, or existing hooks in `.factory/settings.json`. Qwen uses project hooks in `.qwen/settings.json`; it records a turn when the hook supplies `submitted_prompt`. Windsurf uses Cascade hooks in `.devin/hooks.json` (or an existing `.windsurf/hooks.json`), preserving other hooks. Its response hook records the assistant summary and closes the turn.

`trace init` also merges `post-commit` and `post-rewrite` blocks into the repository's Git hooks so commits, amends and rebases are linked to steps. An existing shell hook keeps its name and contents; the block is inserted after its first line and removed by `trace uninstall`. Other existing hooks are renamed with a `.pre-trace` suffix and run first; `post-rewrite` hooks still receive their input. When `core.hooksPath` points inside the working tree (tracked team hooks, or a manager such as husky), Trace leaves those files unchanged and prints the lines to add yourself (`git_hook_setup` in `--json` output).

`trace search` checks recorded prompts, reasoning, and messages by default. Each word must match; use `|` within a word for alternatives, as in `trace search deadlock\|panic sqlite`. `--scope turns` searches prompts and reasoning; `--scope messages` searches conversation messages. `--edited-path 'src/**'` filters turns by paths recorded in their edits and can be used without search words. Search is case-insensitive. The MCP `search_history` tool accepts the same `query`, `scope`, and `edited_path` inputs. `trace dispatch` formats recorded prompts and reasoning without generating new text. `trace clean` preserves baseline and commit-linked keyframes.

After `trace why path/to/file.go:12` or `trace search`, run `trace messages --step N` with the reported step to read that turn's recorded conversation.
`trace why path/to/file.go:12 --messages` shows the first 20 messages from the responsible turn directly; use `trace messages --step N` to read the rest.
Use `trace messages --step N --limit 20 --offset 20` for the second page of a longer turn.

`trace activity --days 7` counts completed turns and distinct edited files over that period and lists up to ten recent sessions. It reads local history only.

In `trace tui`, select a step and press Enter to read its details. Press `d` for its file diff. Press `w` from the timeline or detail view, enter `file:line`, and Trace shows the responsible step and its recorded messages.

`trace why` follows unchanged lines across inserts and deletes. If a very large edit leaves a line's origin ambiguous, it reports `origin uncertain after a large edit` instead of guessing at a step.

Committed lines are followed through `git blame` and the rewrites Trace recorded, so amend, rebase (including interactive squash, fixup and reordering), cherry-pick and `git merge --squash` keep the original step. `--json` reports `commit` (the commit that now contains the line), `origin_commit` (where it was first committed, when different), `rewrite_chain` (each rewrite's `from`, `to` and `kind`, oldest first; kinds are `amend`, `rebase`, `squash`, `cherry-pick`, `merge-squash`, `patch-id` and `inferred`) and `confidence`: `exact`, `normalized` (matched only after ignoring formatting such as gofmt or prettier output), `inferred` (matched by content to a squashed commit that named no sources) or `uncertain`. `--all` also lists formatting-only edits passed on the way to the origin. `trace blame FILE` reports the same fields for every line, plus a count per origin; text output marks rewritten commits with `*`. Both refuse secret and excluded paths. Rewrite tracking needs the Git hooks that `trace init` installs; commits made without them are explained from recorded edits and identical patches only.

## Agent Trace export

`trace export --format agent-trace` writes [Agent Trace](https://agent-trace.dev) 0.1.0 records, one JSON object per line, built from the same line attribution as `trace why` and `trace blame`. Without `--rev` it writes one record for the working copy of every file with recorded edits; that record has no `vcs` entry because its line numbers refer to the working copy (`metadata["dev.usetrace"].head` names `HEAD`). `--rev SHA` writes one record for the lines that commit added or changed, and `--rev A..B` one record per non-merge commit in the range (at most 1000), with `vcs.revision` set to the commit and a record `id` derived from it, so re-exporting a commit gives the same record.

Each file groups its ranges into conversations. A line written by a recorded agent turn becomes contributor type `ai` with the conversation `url` `trace://turn/<turn-id>`; `metadata["dev.usetrace"].turns` maps that URL to the agent name and step. Lines you edited become `human`; lines present before Trace started, or whose origin cannot be established, become `unknown`. Trace does not record model names, so `model_id` is omitted. Records hold paths, line numbers and IDs only, never file contents or prompts. Secret paths and `.traceignore`d paths are never exported. The schema in the specification requires `version` to match `^[0-9]+\.[0-9]+$` while its examples show `0.1.0`; Trace writes `"0.1"`, which validates. With `--out`, the file is written with mode 0600 and `--json` prints `path` and `records`.

## Markdown session export

`trace export --format markdown --out DIR` writes `DIR/<session-id>.md` (mode 0600) for every recorded session, or only the one named by `--session`. Each file lists the agent and times, then for each turn its step, prompt (up to 4000 characters), a response summary (the turn's last recorded assistant message, up to 1500 characters) and the files its recorded edits touched with their edit kinds. Prompts and responses are passed through the redactor again on export; secret paths and `.traceignore`d paths are left out. No file contents, reasoning or tool inputs are written. `--json` prints the written `files`. With no recorded sessions the directory is still created and nothing is written.

## Importing other tools' attribution

`trace import --from git-ai` reads the authorship logs that [git-ai](https://github.com/git-ai-project/git-ai) attaches as Git notes under `refs/notes/ai`, following the [Git AI Standard v3.0.0](https://github.com/git-ai-project/git-ai/blob/main/specs/git_ai_standard_v3.0.0.md) (`schema_version` `authorship/3.*`). It runs only read-only Git commands (`rev-parse`, `notes list`, `cat-file`): it never writes notes, refs or commits. Each attested range becomes a local hint in `.trace/trace.db` with its contributor (`ai` for session and legacy prompt keys, `human` for human keys), the agent's tool and model, and the git-ai session key; human names and emails are not kept, and secret paths are skipped. Notes that do not parse, use another schema version, or are attached to something other than a commit are listed as skipped. Importing again adds nothing. Hints are not synced or exported in bundles. `trace export --format agent-trace --rev ...` uses a hint for a line of the same commit and path only when Trace itself has no origin for it (lines recorded before Trace started, or unknown), emitting git-ai's model as `model_id`; `metadata["dev.usetrace"].imported_hint_lines` counts such lines. Hint line numbers are taken to refer to the annotated commit. Such a conversation has no `url`; its `related` entry (type `git-ai.session`, URL `urn:git-ai:session:<key>`) names the git-ai session, and `metadata["dev.usetrace"].imported_conversations` maps that URN to its tool and model. `model_id` is written as `provider/model` only when the provider is given or unambiguous (`claude-*` → `anthropic`, `gpt-*`, `chatgpt-*`, `codex-*` and `o1`–`o9` models → `openai`, `gemini-*` → `google`); otherwise it is omitted. Imported hints affect only `trace export --format agent-trace --rev`; `trace why`, `trace blame`, the working-tree export and MCP tools do not use them. A note stores at most 20000 ranges, and at most 2000 per path.

`trace import --from entire` is refused: Entire's documentation names its checkpoint refs (`refs/entire/checkpoints/<shard>/<id>`, or the `entire/checkpoints/v1` branch) and its `Entire-Checkpoint` commit trailer, but not the files inside a checkpoint or the trailer's value format, so Trace does not guess at them. A Trace database file named `git-ai` or `entire` can still be imported as `./git-ai`.

## Commit trailers

Trace never changes commit messages unless you opt in. `trace config set commit-trailer on` writes `.trace/commit-trailer` and merges a `prepare-commit-msg` block into the repository's Git hook, the same way `trace init` merges `post-commit` (an existing shell hook keeps its contents; another hook is renamed `prepare-commit-msg.pre-trace`, runs first and still aborts the commit if it fails; hooks under a `core.hooksPath` inside the worktree are left alone and the block to add is printed). When a commit's staged paths were edited by recorded agent turns since `HEAD` was committed, the block adds one `Trace-Turn: <turn-id>` trailer per turn (at most 20) with `git interpret-trailers`, keeping existing trailers and comments. Commits with only your edits, merges, squash merges, amends, `-c`/`-C` reuse of a message, and commits made by cherry-pick, revert or rebase are left unchanged, and the block never fails a commit. `trace config set commit-trailer off` (or `trace uninstall`) removes the block and restores the original hook; without the marker file the block does nothing. `trace uninstall` keeps `.trace/` and therefore the `.trace/commit-trailer` marker, so a later `trace init` reinstalls the block; turn trailers off first (or use `trace uninstall --purge`) to stop that. When Git runs hooks from inside the worktree (`core.hooksPath`, husky), `config set commit-trailer on` exits 0 but prints `Manual step pending` with the block to add (`manual_hooks` in `--json`); trailers are not added until you add it. The turn ID is the same one `trace export --format agent-trace` uses in its `trace://turn/<turn-id>` conversation URLs. Agent Trace 0.1.0 defines no commit trailer or Git notes binding, so Trace adds no `Agent-Trace` trailer.

## Safe restore workflow

```sh
trace log
trace show 2
trace rewind 2 --dry-run
trace rewind 2
```

A real rewind saves a safety keyframe first and prints the command to undo it, also when the restore then fails (`--json` output includes `safety_step` and `error`). Rewind and `--dry-run` refuse to replace an ignored or `.traceignore`d file, a symlink or another path the safety keyframe cannot capture; move it aside or limit the restore with `--files`. `fork` and `resume` launch the selected agent unless `--no-launch` is set.

## Cross-agent resume

`trace resume` continues a session recorded with one agent in any agent with a launch capability, for example after a Claude Code session:

```sh
trace resume 7 --agent codex              # rewind to step 7, start Codex
trace resume 01JD3 --agent cursor --fork  # a session's latest step, in a new worktree
trace resume 7 --agent agy --no-launch    # print the plan and the handoff only
```

The argument is a step number or a session: a Trace session ID, a unique prefix of one, or the agent's own session ID; a session resolves to its latest step. Exact Trace or agent session IDs win over prefixes, and `_`, `%` and `\` in a session name are matched literally. A number is a step, unless it is also a session ID, an agent session ID, or (with at least 4 digits) a prefix of a session ID; then `resume` refuses it as ambiguous and `--step` or `--session` decides. Before anything touches the tree, `resume` checks that the agent has a launch capability (otherwise it fails with the list of agents that do) and, unless `--no-launch` is set, that its program is on `PATH`. Then it prepares the tree: by default a rewind with a safety keyframe (as `trace rewind`), with `--fork` a new worktree beside the repository (as `trace fork`, named `REPO-fork-STEP[-SUFFIX]`), or with `--no-rewind` nothing. Without `--agent`, it starts the session's own agent when that agent can be launched, else `claude-code`.

The handoff is built from Trace history only and contains the source session and agent, the original goal (the session's first prompt), up to 20 latest turn prompts, the files changed since the session started with added/removed line counts, open items found in the last response (unchecked `- [ ]` items, `TODO`/`FIXME` lines and bullets under a "Next steps"-style heading) and how to see more with `trace show`, `trace log`, `trace diff` and the MCP tools `get_turn`, `get_messages` and `search_history`. Prompts are clipped to 500 characters, the file list to 60 entries, and the whole text to `--max-context-bytes` (default 12288, from 1024 to 102400, below Linux's 128 KiB limit for one argument); a cut is marked and never splits a redaction marker. Every field (prompts, responses, open items, file paths) is redacted before it is clipped, and the whole text again afterwards, so history stored before a redaction rule existed does not leak and a clip cannot expose the start of a secret; paths matching the secret excludes are left out, also from `--json` `files` and `todos`. NUL, other control characters except newline and tab, and terminal escape sequences are removed. Line counts read the first 256 KiB of each file and are computed only for the listed files.

The handoff is written to `.trace/context.md` in the target worktree (Git-excluded, mode 0600) and passed as the agent's initial prompt argument. No project instruction file (`CLAUDE.md`, `AGENTS.md`, `.cursor/rules`, `GEMINI.md`) is written or changed:

| Agent | Command |
| --- | --- |
| `claude-code` | `claude "<handoff>"` |
| `codex` | `codex "<handoff>"` |
| `cursor` | `agent "<handoff>"` |
| `agy` | `agy --prompt-interactive "<handoff>"` |

Text output names the step, the undo command or fork path, the command (with the handoff shown as `<handoff context>`) and the context file; `--no-launch` then prints the handoff. `--json` returns `step`, `mode` (`rewind`, `fork` or `in-place`), `path`, `safety_step`, `session_id`, `source_agent`, `agent`, `command`, `context_file`, `context_bytes`, `truncated`, `files`, `todos` and `launched`, plus `context` with `--no-launch`. Rewinding still writes a safety keyframe even with `--no-launch`; use `--no-rewind --no-launch` for a plan without side effects other than the context file.

## Independent review

`trace review` sends the redacted diff of unstaged changes (`--staged`: the index) to one or more headless reviewers, each in its own session and process group:

```sh
trace review --agents claude-code,codex                          # independent reviews, printed as written
trace review --agents claude-code,codex --judge claude-code      # plus one consolidated, ranked list
trace review --agents claude-code,codex --format sarif > review.sarif
trace review --agent codex --with-provenance --format markdown   # PR-comment-ready, with recorded intent
```

Claude reviews run with built-in tools and inherited MCP servers disabled (`--tools "" --strict-mcp-config`); Codex runs in its read-only sandbox. The judge runs the same way. Secret paths are excluded, file paths are literal, the diff is capped at 64 KiB, text that would close an evidence block (`</untrusted`) is escaped, and each run (reviewer or judge) has the `--timeout` deadline.

Without `--judge`, `--with-provenance` or a `--format` other than `text`, the prompt and output are unchanged from earlier releases. With any of them, reviewers are asked to answer with JSON in this schema:

```json
{"findings": [{"file": "a.go", "start_line": 4, "end_line": 6, "severity": "critical|high|medium|low|info",
               "title": "one line", "detail": "explanation", "reviewers": ["claude-code", "codex"]}]}
```

Answers are parsed tolerantly (a bare array, a fenced block or JSON surrounded by prose; `path`/`line` aliases; numeric strings; severity synonyms such as `major` or `nit`; unknown severities become `medium`). Paths that are absolute or leave the repository are dropped from a finding, and at most 200 findings per answer are kept. An answer that is not in the schema is kept as raw text.

`--judge NAME` runs one more agent after the reviewers finish. It receives the diff summary (paths and added/removed counts) and every completed reviewer's findings, or its raw text, redacted and capped at 64 KiB in total (24 KiB per reviewer, dropping the least severe findings first), and returns a deduplicated, severity-ranked list. Reviewer names the judge did not receive are discarded and agreement counts are recomputed from the rest. If no reviewer completed, the judge is skipped. If the judge fails or does not answer in the schema, reviewer findings are merged locally instead (same file, overlapping lines and the same normalized title) and `findings_source` is `merged`; the judge's raw text is still shown.

`--with-provenance` requires recorded history. For each changed hunk it adds the recorded turns that wrote its added lines, explained as in `trace why`: step, agent, the share of added lines, and the turn's prompt, redacted and then capped at 1 KiB, each prompt shown once. At most two turns are listed per hunk, the whole block is capped at 16 KiB, and omitted hunks are counted. Reviewers are asked to compare that intent with the implementation. With `--staged`, a file whose index content differs from the working tree is listed as unavailable.

`--format markdown` prints a pull request comment: a findings table with severity, location and agreement (for example `2/2`), collapsible details, failed runs and any raw answers. Model text is escaped so it cannot add HTML, links, tables or `@` mentions. `--format sarif` prints a SARIF 2.1.0 log (rule `trace-review/finding`; `critical`/`high` map to `error`, `medium` to `warning`, `low`/`info` to `note`; raw answers use `trace-review/unstructured`; failed runs set `executionSuccessful: false`). Locations are repository-relative URIs under `%SRCROOT%`. Both formats need launched reviewers and cannot be combined with `--json` or `--no-launch`.

`--json` keeps the existing report and, in structured mode, adds `findings` to each result, a `judge` result, the consolidated `findings` and `findings_source` (`judge` or `merged`). `--no-launch` prints the reviewer prompts and, with `--judge`, the judge prompt template. The command exits non-zero when any reviewer or the judge fails or times out; completed results are still printed.

## Automation

`--json` output is intended for scripts. Step numbers remain stable even when retention removes an older step. The global flag does not change the MCP stream or internal agent hooks.

Sync is off until login. `trace login` opens a browser and returns to the running CLI after approval. Use `--device-auth` for remote/headless sessions. Add `--auto-sync` to upload existing and new history in the background; use `trace sync disable-auto` to turn it off. See the [sync guide](sync.md) for passphrases, access control, and recovery.
