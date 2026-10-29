# CLI reference

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

For Codex, `trace setup --agent codex` installs user-level hooks in `$CODEX_HOME/hooks.json` when `CODEX_HOME` names an existing directory, or `~/.codex/hooks.json` otherwise. Relative paths resolve from the current working directory. Use the same profile for removal. Review and trust the hooks with `/hooks` in Codex; disabled hooks or managed-only policy can prevent them from running. Hook events use the session's reported working directory to select the Git worktree, and an `Interrupt` event closes the current recorded turn after requesting a file scan. The older project transcript poller also uses the `CODEX_HOME` inherited by its daemon; restart that daemon after switching profiles. While both sources run, the daemon favors hook events after observing that event kind for the same session and tool name, while retaining transcript context and fallback for kinds not yet observed. Context and events that reach the poller first can still overlap, and changing hook trust during a session may require a daemon restart for transcript fallback. Rerun setup after upgrading Trace to add the interruption hook to an existing installation.

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
| `trace why FILE:LINE [--all] [--messages]` | Find the change responsible for a line and optionally show its conversation |
| `trace fork STEP [--agent NAME] [--name SUFFIX] [--no-launch]` | Create a worktree at a step |
| `trace resume STEP [--agent NAME] [--no-launch]` | Rewind and continue with an agent |
| `trace status` | Show daemon, session, and storage status |
| `trace activity [--days N]` | Summarize local turns, sessions, changed files, and agent activity |
| `trace search [TERMS...] [--scope all\|turns\|messages] [--edited-path GLOB] [--limit N]` | Search completed turns by text and edited path |
| `trace messages [--step N] [--session ID] [--limit N] [--offset N]` | Read recorded conversation, optionally filtered and paged |
| `trace diff STEP STEP [--content]` | Compare two keyframes |
| `trace dispatch [--limit N]` | Print a chronological summary of recent local turns |
| `trace agent add NAME` | Enable one agent adapter in an initialized repository |
| `trace agent remove NAME` | Remove one agent adapter and its installed hooks |
| `trace agent-help` | Print commands available to coding agents |
| `trace doctor [--bundle FILE]` | Check setup; bundle contains check results, not daemon log contents |
| `trace clean [--force]` | Preview removal of unlinked local history; apply with `--force` |
| `trace tui` | Open the local timeline viewer |
| `trace worktree gc` | Remove eligible Trace-created worktrees |
| `trace gc [--keep-days N] [--dry-run]` | Remove expired history and unused blobs |
| `trace update-check` | Check for a release, at most once per day |
| `trace export --format agent-trace --out FILE` | Export line attribution as Agent Trace JSONL |
| `trace login --remote OWNER/REPO --mode pro\|team --endpoint HTTPS_URL [--auto-sync]` | Authorize optional sync with GitHub device flow; opt into background upload |
| `trace sync [--watch]` | Upload local history to the configured sync server |
| `trace sync disable-auto` | Turn off background sync for this repository |
| `trace sync pull --into PATH` | Recover synced history into a fresh Git checkout |
| `trace daemon start` | Start the local daemon if it is not running |
| `trace daemon stop` | Stop the local daemon |
| `trace mcp` | Serve read-only MCP tools over stdio |

Agent names for `init` and `agent add` include `claude-code`, `codex`, `cursor`, `agy`, `opencode`, `windsurf`, `gemini`, `copilot`, `qwen`, `factory`, `pi`, `omp`, `grok`, `goose`, `amp`, `openclaude`, `openhands`, `kilo`, and `kiro`. OpenHands project integration requires an existing `.openhands/hooks.json`. Re-run `trace init` or use `trace agent add` to enable another adapter in the same repository. `trace agent remove` removes only the named adapter; `trace uninstall` removes all integrations. Copilot project hooks use `.github/hooks/usetrace.json`; refresh older installations. Kilo project plugins request a full edit scan at session idle and use bounded asynchronous event delivery; queued events can be lost on exit or overflow. Refresh with `trace agent add kilo`. Kilo CLI plugin loading and turn capture remain unverified. Grok project hooks require folder trust. Amp uses a project plugin and may require plugin activation. Goose uses a project plugin. Pi and Oh My Pi use project TypeScript extensions under `.pi/extensions/trace/` and `.omp/extensions/trace/`. Factory Droid uses `.factory/hooks.json`, or existing hooks in `.factory/settings.json`. Qwen uses project hooks in `.qwen/settings.json`; it records a turn when the hook supplies `submitted_prompt`. Windsurf uses Cascade hooks in `.devin/hooks.json` (or an existing `.windsurf/hooks.json`), preserving other hooks. Its response hook records the assistant summary and closes the turn.

`trace search` checks recorded prompts, reasoning, and messages by default. Each word must match; use `|` within a word for alternatives, as in `trace search deadlock\|panic sqlite`. `--scope turns` searches prompts and reasoning; `--scope messages` searches conversation messages. `--edited-path 'src/**'` filters turns by paths recorded in their edits and can be used without search words. Search is case-insensitive. The MCP `search_history` tool accepts the same `query`, `scope`, and `edited_path` inputs. `trace dispatch` formats recorded prompts and reasoning without generating new text. `trace clean` preserves baseline and commit-linked keyframes.

After `trace why path/to/file.go:12` or `trace search`, run `trace messages --step N` with the reported step to read that turn's recorded conversation.
`trace why path/to/file.go:12 --messages` shows the first 20 messages from the responsible turn directly; use `trace messages --step N` to read the rest.
Use `trace messages --step N --limit 20 --offset 20` for the second page of a longer turn.

`trace activity --days 7` counts completed turns and distinct edited files over that period and lists up to ten recent sessions. It reads local history only.

In `trace tui`, select a step and press Enter to read its details. Press `d` for its file diff. Press `w` from the timeline or detail view, enter `file:line`, and Trace shows the responsible step and its recorded messages.

`trace why` follows unchanged lines across inserts and deletes. If a very large edit leaves a line's origin ambiguous, it reports `origin uncertain after a large edit` instead of guessing at a step.

## Safe restore workflow

```sh
trace log
trace show 2
trace rewind 2 --dry-run
trace rewind 2
```

A real rewind saves a safety keyframe first and prints the command to undo it. `fork` and `resume` launch the selected agent unless `--no-launch` is set.

## Automation

`--json` output is intended for scripts. Step numbers remain stable even when retention removes an older step. The global flag does not change the MCP stream or internal agent hooks.

Sync is off until login. Add `--auto-sync` to `trace login` to upload existing and new history in the background; use `trace sync disable-auto` to turn it off. See the [sync guide](sync.md) for passphrases, access control, and recovery.
