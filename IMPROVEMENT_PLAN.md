# Trace — Improvement Plan (DeltaDB-inspired + competitive)

Created 2026-10-29. Tracks the "do all" request: port remaining high-value
features from DeltaDB and close competitive gaps identified vs Entire / Claude
Code /rewind / codex-rewind.

## Status legend
- [x] done
- [~] in progress
- [ ] pending

## Phase I1 — Quick wins (done)
- [x] `trace init --if-needed`
- [x] `trace diff <a> <b>` (+ `--content`)
- [x] Per-delta actor attribution (migration 7)
- [x] Conversation-as-history (migration 8, `trace messages`, `get_messages` MCP)
- [x] Navigate from a step to its conversation with `trace messages --step N`
- [x] Navigate from a changed line to its conversation with `trace why --messages`
- [x] `trace worktree gc`
- [x] Watcher graceful EMFILE handling
- [x] `trace doctor`
- [x] Lazy baseline in daemon
- [x] `trace export --publish`

## Phase I2 — Ephemeral worktree lifecycle
- [x] Auto-init on `trace fork` (already existed)
- [x] Auto-remove Trace-created worktrees on session end when clean (behind env var `TRACE_AUTO_CLEANUP_WORKTREES=1`)
- [x] Auto-remove on daemon idle shutdown (deferred: risky to remove cwd; manual `worktree gc` covers it) — documented as intentional
- Depends on: gitx.WorktreeRemove (done)

## Phase I3 — `trace tui` (local timeline viewer)
- [x] Add `github.com/charmbracelet/bubbletea` + `lipgloss` (approved)
- [x] Screens: step list, turn detail
- [x] File diff view in TUI and why-line view with linked conversation

## Phase I4 — More agent adapters

Hook transport batches multi-event callbacks into one redacted line when the active daemon supports it and retains the individual-event format for older daemons. A nonblocking spool lock falls back to atomic redacted overflow files; contention and ordered replay have tests. Spool frames remain until processing succeeds, so a failure or interrupted replay may repeat earlier events; a failed processing callback stops the daemon and leaves the failed events for a later restart when spooling succeeds. Socket events already read during a backlog failure are also appended to the spool when possible; a crash before processing or spooling socket events is still not durably acknowledged. Damaged or over-4 MiB shared-spool lines are skipped; damaged or oversized overflow files are retained with an `.invalid` suffix, and later frames continue. Older daemons do not ingest overflow files until restarted after upgrade; concurrent event ordering and crash recovery still need broader verification.

Event sanitization also redacts token-shaped metadata before spooling, hashes sensitive session IDs deterministically for correlation, and drops sensitive event and transcript paths. Redaction remains pattern-based rather than a guarantee that every possible secret is recognized.

The hook performance script also measures Cursor global reads and Gemini global multi-event completion, including payload-based repository lookup. A local 100-sample check measured p95s of 11.33 ms and 11.43 ms respectively; timings vary by host and do not establish worst-case latency.

Assistant context delivered by agent hooks is merged with available transcript context and saved in the conversation atomically with the completed turn. An Antigravity prompt recovered after turn start is also added to the conversation. Existing historical turns are not backfilled; sync currently transfers turn summaries but not message rows.

Claude Code: global setup respects `CLAUDE_CONFIG_DIR`; stop, API failure and session end request a scan and close open turns, while subagent lifecycle events do not close the parent turn. Refresh hooks with `trace setup --agent claude-code` or `trace agent add claude-code`. Version 2.1.312 ran an identical user/project SessionStart command once in an isolated startup test, including with different matching groups; other events and versions still need overlap verification.

Codex: global hooks and the legacy project transcript poller use `CODEX_HOME` when set. The daemon favors observed hook event kinds over matching transcript events for the same session and tool, retaining context and fallback for unobserved kinds. Poller-first delivery, context, and trust changes can still cause overlaps or gaps; live verification remains. Review and trust hooks in Codex.

Cursor: project and preview user-level hooks route single-folder local workspaces from the payload; read hooks return an explicit allow response even on early exit. Subagent completion records available redacted summary context and requests a scan when the parent turn closes, without closing it. Stop and session end request a scan and close open turns. Refresh hooks with `trace setup --agent cursor` or `trace agent add cursor`. Cursor describes sessionEnd as fire-and-forget, so delivery is not guaranteed. Multi-root, cloud sessions, imported Claude hooks, stop-hook continuations, and live verification remain outstanding.

Gemini CLI: project and preview user-level hooks select the worktree from the session payload; global hooks avoid matching project hooks, including when settings contain `hooks.enabled` or `hooks.disabled`. An isolated 0.25.2 API-failure turn recorded after setting `hooks.enabled=true`; that version defaults to disabled, and global setup, project init and agent add report the requirement without enabling other hooks. AfterAgent and SessionEnd request a scan and close open turns; relative tool paths use the session directory. SessionEnd is best effort upstream, and another AfterAgent hook can request a retry after Trace records a boundary. Validate successful model turns, workspace trust behavior, and lifecycle ordering.

Qwen Code: project and preview user-level hooks use the session working directory and distinguish submitted prompts from continuation messages. Global setup honors the `QWEN_HOME` environment override. Qwen can also load that override from home-scoped `.env` files; Trace setup does not read those files, so export it when running setup. Known subagent hooks are ignored; stop, failure and session end request a scan before closing open turns. Relative tool paths use the session directory. Installed Qwen 0.0.14 emitted no Trace hooks in an isolated failed-API test and its code lacks the hook events; setup reports the version requirement. Validate hook discovery on a newer release and workspace trust behavior.

Pi and Oh My Pi: preview global extensions route sessions by their working directory; Pi 0.65.0 recorded an offline test turn, including with its project extension loaded. Oh My Pi 18.1.18 recorded an offline test turn and loaded the global extension from a named profile with a custom config root; project extensions now bound subprocess delivery and request a scan for custom-tool changes; unknown-path edits trigger a full scan even when watcher events are present; delivery under real workloads still requires verification.

Amp: preview system plugin resolves the local workspace through Amp's plugin API and requests a scan at turn end. Both project and system plugins capture available final assistant text and modified paths identified by Amp's tool-result helper, without retaining raw tool arguments. The project plugin takes precedence and also requests a scan and uses bounded asynchronous delivery; unchanged older plugins are upgraded while user edits are preserved. An isolated `amp plugins list` attempt could not connect to Amp's service. Validate live loading, event timing, and delivery at exit.

### Ten additional coding agents (prioritized 2026-10-29)

Each integration requires an event source that identifies the repository, session, prompt, and completed turn; merge-preserving installation and removal; redaction before spooling; fixture and lifecycle tests; and a live test when the agent is available. Record partial coverage as preview and avoid inventing turn boundaries. Preserve the existing work on CrewAI and Hermes from Entire's external list.

1. **Kimi Code CLI** — preview implemented through user-level hooks. Edited managed hook blocks are preserved; stop, failure, interruption and session end request a scan before closing turns, and available final response text is captured. Relative tool paths resolve against the session directory. An isolated Kimi 0.26.0 run with an unresponsive offline provider recorded a structured prompt and closed its turn on session end after interruption; successful model turns, normal completion events and transcript import remain unverified. [Hook reference](https://moonshotai.github.io/kimi-code/en/customization/hooks).
2. **Cline** — CLI preview hooks implemented for global and project setup. Global routing requires one workspace root and yields only to unchanged project hooks targeting that repository. Install and removal preserve edited and symlinked hooks and recognize unchanged hooks after a binary or repository move. Completion captures available SDK response text; completion, error, cancellation and shutdown request a scan before closing open turns; tool payloads accept mixed parameter types. Live CLI ordering, reported prompt-hook gaps, transcript import, and editor support remain. [CLI SDK hook lifecycle](https://github.com/cline/cline/blob/main/sdk/examples/plugins/README.md), [hook discovery](https://github.com/cline/cline/blob/main/sdk/packages/shared/src/storage/paths.ts).
3. **Mistral Vibe Code** — preview single-prompt launcher captures a baseline and execution around `vibe -p`; bounded redacted CLI output is recorded only when neither output stream is a character device, preserving terminal behavior. Interactive use still needs journal observation; check legacy and unified formats, disabled logging, resumed sessions, stop-hook retries, and subagents. Current Vibe exposes pre-tool, post-tool and post-agent hooks but no prompt-start hook; older hook names differ. [Hooks](https://docs.mistral.ai/vibe/code/cli/hooks), [session storage](https://github.com/mistralai/mistral-vibe/blob/main/README.md).
4. **Zoo Code** — preview VS Code observer packages task lifecycle events after checking the optional history workspace against local editor folders; user installs the VSIX once. Completion-result asks end turns and their feedback starts another; other mid-task feedback remains grouped. The extension event schema declares `taskUserMessage`, but the current API listener does not forward it. Verify editor installation, event delivery for sidebar and tabs, startup timing, resumed tasks, and subtask identity before claiming full support. [Event schema](https://github.com/Zoo-Code-Org/Zoo-Code/blob/main/packages/types/src/events.ts), [extension API](https://github.com/Zoo-Code-Org/Zoo-Code/blob/main/src/extension/api.ts).
5. **Augment Auggie** — preview global hooks implemented for prompt, session, tool and stop events, resolving workspaces from the event payload and skipping sessions spanning multiple Git worktrees. Reported file-change paths are captured without contents. Stop and session end request a scan before closing open turns; available optional stop response text is redacted and recorded, without enabling extra metadata. Prompt and stop hooks run asynchronously, so ordering and pre-edit snapshots are not guaranteed. Verify actual CLI lifecycle and transcript import. [Hooks](https://docs.augmentcode.com/cli/hooks).
6. **OpenHands** — preview global CLI hooks implemented, including the SDK's `OH_PERSISTENCE_DIR` override. SDK hook `working_dir` selects the repository and resolves relative tool paths; stop and session end scan before closing open turns. Existing project hook files take precedence and can be merged with `trace init --agent openhands` or `trace agent add openhands`. Live CLI, hosted sessions and transcript import remain unverified. [Hook implementation](https://github.com/OpenHands/software-agent-sdk/tree/main/openhands-sdk/openhands/sdk/hooks).
7. **Continue** — preview single-prompt launcher captures `cn -p` executions. Interactive lifecycle remains unverified; the original hook-system change added helpers without agent lifecycle call sites. [Headless mode](https://docs.continue.dev/cli/headless-mode), [hook-system changes](https://github.com/continuedev/continue/pull/11029/files).
8. **Aider** — preview single-message CLI launcher and wrapper for explicit Python `Coder.run` calls, with optional bounded queue flushing. The wrapper records bounded returned response text; refresh unchanged bridges with `trace agent add aider`. Positional and `with_message` keyword calls were checked against installed Aider 0.86.2 with an offline stub for the model operation. Interactive CLI sessions and a live model turn remain unverified. [Options](https://aider.chat/docs/config/options.html), [scripting](https://aider.chat/docs/scripting.html).
9. **Crush** — preview single-prompt launcher captures `crush run` executions; interactive lifecycle remains unsupported. Investigate its session event stream; current hooks expose PreToolUse but not a turn-completion hook. [Run command](https://github.com/charmbracelet/crush/blob/main/internal/cmd/run.go), [hooks](https://github.com/charmbracelet/crush/blob/main/docs/hooks/README.md).
10. **Codebuff / Freebuff** — preview wrapper implemented for explicit Codebuff SDK `client.run` calls, with optional `flush()` at exit; interactive Codebuff and Freebuff CLI sessions remain unsupported. Verify against live SDK. [SDK](https://github.com/CodebuffAI/freebuff/blob/main/sdk/README.md).

Additional requested agent: **Poolside `pool`** — preview global hooks implemented for the documented CLI 1.0.16 protocol. Setup merges ordinary block-style existing hook lists while preserving other settings; flow-style and complex YAML require manual merging. Setup reports the version requirement and restart step, and warns when the installed version is older (this machine has 1.0.14). Stop requests a scan before ending the turn, and relative tool paths use the session working directory. Cancelled turns have no stop hook, and other stop hooks can request continuations without another prompt event; live verification remains. [Hook reference](https://docs.poolside.ai/hooks).

- Roo Code was sunset and archived; do not add it as an active target.
- [ ] Kimi Code CLI: preview user-level lifecycle hooks implemented; generated configuration validated with Kimi doctor. Verify live turn capture and transcript handling.
- [ ] OpenClaude: preview hook adapter and global setup implemented; global events and relative edit paths use the hook payload's working directory. Stop, failure and session end scan before closing open turns, while subagent lifecycle events do not close the parent turn. Refresh existing hooks with `trace setup --agent openclaude` or `trace agent add openclaude`. Failure hooks are asynchronous upstream, so ordering can race a subsequent prompt. Synthetic end-to-end coverage exists; verify lifecycle and recording with the actual CLI. Native transcript import remains unverified. Its license identifies proprietary-derived code; use documented interfaces without copying source.
- [~] OpenCode: legacy project database poller uses read-only access, waits for schema creation, and tracks repository-scoped updates with a one-minute overlap without closing tool-continuation steps; updates committed more than a minute behind the latest timestamp can still be missed. Preview global observers target v1 and v2; v1 uses prompt, tool and idle hooks and yields to the project poller, while v2 uses inbox delivery/execution events and captures assistant text. Verify live loading and turn capture, concurrent prompts, session moves, and v2 interaction with the legacy poller.
- [ ] Aider: preview single-message CLI launcher and Python `Coder.run(message)` wrapper implemented; interactive CLI, overlapping edit attribution, and live verification remain.
- [ ] Continue: preview headless launcher implemented; interactive hooks, runtime event emission, and released-version coverage require verification.
- [ ] Cline: CLI hook preview implemented; verify live event ordering, prompt capture, transcripts, and separate editor support.
- [~] Windsurf Cascade: workspace and preview user-level hooks for prompt, read, write, shell, and response; merge-preserving install/uninstall. Live delivery, asynchronous turn ordering and multi-repository routing require verification; Devin Local is separate.
- [x] Gemini CLI: expose existing hook adapter in init/agent management and capture final response
- [~] Copilot CLI: project hooks in `.github/hooks/usetrace.json` and preview global hooks in `$COPILOT_HOME/hooks/usetrace.json`; setup requires an absolute `COPILOT_HOME` so it does not vary by launch directory. Session end requests a scan and closes open turns; identified subagent lifecycle events do not close the parent turn. Global hooks yield to unchanged Trace project hooks for the same event; if workspace admission prevents those project hooks from loading, the event may be missed. Merge-preserving install/uninstall; older project installs require refresh. An isolated Copilot 0.0.351 attempt stopped at authentication before hooks could be verified; live turn capture and hook admission remain unverified.
- [ ] Qwen Code: project and preview user-level hooks for session, submitted prompt, tool, stop, failure, and session end; live verification remains. Normal `prompt` is not treated as a user turn because Qwen may invoke this hook for continuations.
- [~] Factory AI Droid: project and preview global hooks preserve existing standalone or settings hooks and route events using `FACTORY_PROJECT_DIR` when provided, falling back to the payload working directory. Idle notifications and session end close open turns. Subagent completion records available string results and requests a scan at parent turn end. Refresh hooks with `trace setup --agent factory` or `trace agent add factory`. Live lifecycle and project/global overlap need verification; migrate legacy hook files with Droid before setup.
- [~] Pi: project and preview global TypeScript extensions for session, prompt, tool, and turn events; response text from agent_end. Native transcript import and live CLI verification remain follow-up work.
- [ ] Kiro: CLI V3 project and global hooks implemented; native `fs_read`, `fs_write`, and shell tool names are recognized, hook payload cwd selects the repository, and edited or symlinked hook files are preserved. Session identity and hook name are validated, and structured tool data in `USER_PROMPT` is not mistaken for a prompt. Global hooks yield to an unchanged Trace project hook for the same event; older global setups must be refreshed with `trace setup --agent kiro`. The generated hooks omit SessionEnd, which is absent from the documented V3 trigger list. Some Kiro versions have reported empty prompt hook payloads; validate hook acceptance, project/global overlap in Kiro, live lifecycle and transcript mapping.
- [ ] Amp: project plugin and preview machine-wide plugin record session and turn events; transcript import and live CLI verification remain.
- [ ] LangGraph: preview callback bridge implemented; live framework verification and reliable flush at process exit remain. CrewAI: preview listener installed per repository; correlated kickoff execution capture implemented, live framework verification and reliable exit flush remain.
- [x] Grok Build: project hooks record session, prompt, tool, and completion events; project folder trust is required. Transcript import and live CLI verification remain follow-up work.
- [~] Oh My Pi: project and preview global TypeScript extensions for session, prompt, tool, and turn events. Native transcript import and live CLI verification remain follow-up work.
- [ ] Kilo: project and preview global plugins capture available assistant response parts, read paths and edit/shell/patch tool names, and request a scan at session idle, and use bounded asynchronous delivery; global routing uses the session directory; loaded project plugins signal the global observer per workspace, while plugins from other worktrees do not claim the session. Project install/removal now recognizes the complete managed file and preserves modified or linked plugins. Kilo 7.8.1 started in an isolated home, but its debug configuration command could not finish after a model catalog network failure; live lifecycle verification, overlap behavior, and local SDK session access remain.
- [~] Goose: project plugin records session, prompt, namespaced developer tool and turn events, captures available response text, and scans on stop or session end. Project hooks resolve the repository from Goose's plugin root instead of the server process working directory; modified plugin manifests are preserved. Session export, live CLI verification, and safe global repository routing remain follow-up work: Goose lifecycle payloads do not always include a working directory.
- [ ] Hermes Agent: preview profile-scoped observer installed with `trace setup --agent hermes`; explicit Hermes enablement and `TRACE_HERMES_REPO` are required. The observer uses a bounded asynchronous queue; verify live lifecycle, repo identity, interruptions, and exit flushing.
- [x] `trace clean` (Entire `clean --force` equivalent)
- [x] `trace agent-help` (agent-facing help, Entire `agent-help` equivalent)
- [x] Plugin loader (`trace-<name>`) already present (Entire plugin model)
- Pattern: verify docs → record fixtures → map to 6 events → install/uninstall → e2e

## Phase I5 — Chunk-level blob dedup
- [x] Fixed-size chunking (32KB) in blob.Store with backward-compatible read (legacy whole-file objects still readable)
- [x] Content-defined chunking (CDC) via Rabin rolling hash (min 8KB, max 64KB, target 32KB)
- [x] Storage format migration (manifest-based, legacy fallback)

## Phase I6 — Cross-machine `trace why`
- [x] Works via sync pull (ops/turns/sessions already synced; why queries by path, no worktree filter)
- [ ] Optional: resolve original machine paths in synced why output

## Phase I7 — Publish-to-repo (done)
- [x] `trace export --publish`

## Phase I8 — Ported from Entire OSS (entireio/cli, entireio/external-agents)
- Agent count as of 2026-10-29: Entire lists 8 native integrations in `cli` plus 10 implemented adapters in `external-agents` (18 total). Trace exposes 19 selectable built-in adapters including OpenClaude, Kilo, Kiro, and the LangGraph preview. Kilo and Kiro need live verification; LangGraph has a preview callback; CrewAI and Hermes remain to implement.
- Full coverage of the previously compared Entire list would mean 21 Trace integrations, including Gemini CLI, Windsurf, and OpenClaude. CrewAI and Hermes Agent remain unimplemented; the LangGraph callback is preview.
- [x] `entire search` → `trace search` (FTS5 keyword search)
- [x] Delta `thread search` → local message search and `--edited-path` filter in CLI and MCP (migration 9)
- [x] `entire agent add` → `trace agent add`
- [x] `entire dispatch` → `trace dispatch`
- [x] `entire activity` → local `trace activity` overview
- [x] `entire doctor` + `doctor bundle` → `trace doctor` + `--bundle`
- [x] Plugins (`entire-<name>`) → Plugins (`trace-<name>`)
- [x] External agent protocol: documented; plugin loader exists
- [ ] Semantic search: requires a suitable local embedding model/runtime and approval for a new direct dependency. Keyword FTS5 search remains available.
- [ ] Trails/PR review with evals — platform feature, out of scope
- [ ] Distributed git hosting — out of scope
- [ ] Continuous memory (entire-brain) — out of scope

## Execution order
I1 done → I2 → (approval) I3 → I4 (one adapter at a time) → I5 → I6

## Constraints
- No new direct dependencies without approval (bubbletea for I3).
- No cgo in default build.
- Every phase ends with `make lint test` passing.
- Hooks never block; daemon local-only.

## Integration notes

- Pi extension API: https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/extensions.md
- Factory Droid hooks: https://docs.factory.com/harness/hooks
- Qwen Code hooks: https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/hooks.md
- Copilot CLI hook schema and workspace config: https://docs.github.com/en/copilot/reference/hooks-reference
- Windsurf Cascade hook schema and workspace config: https://docs.devin.ai/desktop/cascade/hooks
- Aider history files and watch mode: https://aider.chat/docs/config/options.html and https://aider.chat/docs/usage/watch.html
- Continue headless output: https://docs.continue.dev/cli/headless-mode
- Cline SDK event stream: https://docs.cline.bot/cline-sdk/sessions
- Hosted sharing and PR review need the separate private `trace-platform` service and an explicit product design for permissions and secret handling; the CLI's commit-ready `trace export --publish` is available locally.
