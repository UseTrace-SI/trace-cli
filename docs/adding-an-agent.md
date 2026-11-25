# Adding an agent

Trace's agent integrations live in `internal/adapters`. The CLI, the daemon and
the engine never name an agent. They iterate the registry in
`internal/adapters/registry.go` and ask each adapter which capabilities it
implements. Adding an agent therefore takes:

- **one adapter file**, `internal/adapters/<agent>.go`;
- **one registry entry** in `registry.go`;
- **the agent's own package**, `internal/adapters/<agent>/`, for its payload
  parser, its configuration installer and any plugin or bridge source;
- **test data**: unit tests in that package, hook payloads and list entries
  in the golden tests, and a README row.

No file in `internal/cli`, `internal/engine` or `internal/daemon` changes. Outside
`internal/adapters`, only the golden test data and the documentation do.

## 1. Write the agent package

Put agent-specific code in `internal/adapters/<agent>/`. This usually means:

- `parse.go` turns one hook payload into `event.Event`s, sanitized and with
  paths resolved against the repository root;
- `install.go` merges Trace's hooks into the agent's project or user
  configuration and removes them again. Merge, never overwrite (AGENTS.md
  rule 7), and keep non-Trace entries byte-for-byte;
- table-driven tests, with recorded payloads in `testdata/`.

Agents whose plugin or bridge sends Trace's generic payload (`session_id`,
`prompt`, `paths`, `tool_name`, `text`, ...) need no parser. Their adapter
embeds `GenericHooks`.

## 2. Write the adapter

`internal/adapters/<agent>.go` defines a type that implements `Adapter`.
Embed `Hooks` (or `GenericHooks`) for the hook defaults, and `Global` for the
`GlobalInstaller` notice defaults. Then override what differs:

| Method | Default | Override when |
| --- | --- | --- |
| `Name()` | required | The name used in commands, events and hook commands |
| `Capabilities()` | required | Display name and `Verified`/`Preview`. This is documentation metadata: no command prints it, and `TestVerifiedAgentsMatchReadme` checks it against the README |
| `HookAliases()` | none | User-level hooks invoke another name, such as `<agent>-global` |
| `ResolveHookRoot` | working directory | The payload or environment names the workspace |
| `SkipHook` | never | A project hook of the same agent reports the event instead |
| `StartsDaemon` | common events only | The agent's own session or turn start event should restart a stopped daemon |
| `DecodeHook` | required (`GenericHooks` provides one) | The agent sends its own payload format |
| `InvalidPayloadMessage` | silent | The agent should see a message on stderr |
| `FramePerEvent` | one batch per callback | Callbacks can produce many events |
| `HookResponse` | no stdout | The agent waits for a reply on stdout |

Hooks must stay within the 20 ms p95 budget and always exit 0 (AGENTS.md
rule 1). `DecodeHook` must not run Git or other processes.

Then implement the optional capabilities the agent supports. Each one is an
interface in `adapter.go`:

| Interface | Used by |
| --- | --- |
| `ProjectInstaller` (`InstallProject`, `RemoveProject`) | `trace init --agent`, `trace agent add/remove`, `trace uninstall` |
| `ProjectChecker` | `trace init` checks the configuration before changing anything |
| `ProjectNoticer` | `trace init` and `trace agent add` print a follow-up step |
| `MarkerEnabled` | An agent enabled by a `.trace/<marker>` file instead of project hooks |
| `SessionReader` | The daemon reads the agent's own session files while the marker exists. `HooksSupersedeSessions` drops session-reader events of a kind the agent's hooks already delivered |
| `GlobalInstaller` | `trace setup`, `trace enable/disable`, `trace agents` (detection via `Integration`) |
| `TranscriptReader` | The engine reads the transcript named by hook events when a turn ends: prompt, reasoning, tool calls, files read and written, tokens |
| `ProjectEvidencer` | `trace doctor` recognizes the installed project integration |
| `Launcher` | `trace fork` and `trace resume` start the agent interactively with a context prompt. `LaunchCommand` must pass the context as a prompt argument (or write it to a temporary file the agent reads); it must never write the project's instruction files. Add the command to the table in [Cross-agent resume](cli.md#cross-agent-resume) |
| `Reviewer` | `trace review` runs the agent headless and read-only, with the prompt on stdin |
| `Runner` | `trace run <agent> --prompt` runs one headless prompt |

The interfaces document their exact contracts. Look at an existing adapter
with the same shape: `cursor.go` (project and global hooks), `codex.go`
(marker, session reader, launcher, reviewer), `aider.go` (bridge and
runner), `vibe.go` (runner only).

## 3. Register it

Add the adapter to `registry` in `registry.go`. Its position is observable:

- global installers are listed by `trace agents` and in `trace setup --help`
  in registry order;
- runners appear in `trace run --help` in registry order.

`projectRemovalOrder` fixes the order in which `trace uninstall` removes
existing project hooks. A new project installer does not need an entry
there: unlisted installers are removed after the listed ones, in registry
order.

## 4. Add golden coverage

The golden tests in `cmd/trace` run the built binary and compare everything
it prints and writes with `cmd/trace/testdata/golden`.

1. Add representative hook payloads, including a malformed one, to
   `testdata/golden/hook_payloads.json`. Every registered agent needs at
   least one.
2. Add the name to each list in `cmd/trace/golden_*_test.go` that applies:
   `projectAgents`, `globalAgents`, `hookAliases`, `launchAgents`,
   `reviewAgents`, `runAgents`. Add the agent's existing user files to
   `projectSeeds` or `globalSeeds`, and its program to `agentPrograms`.
   `TestGoldenAgentListsMatchRegistry` fails until these lists match the
   registry.
3. Record the new cases and review the diff:

   ```sh
   go test ./cmd/trace -run Golden -update-golden
   git diff cmd/trace/testdata/golden
   ```

   Only cases for the new agent, and help text that lists it, should change.

## 5. Document it

- Add a row to the README integration table only after live verification,
  and set `Verified` in `Capabilities` at the same time.
- Describe the setup and its limits in `docs/cli.md` or `docs/quickstart.md`.

Run `make lint test` before review.

## What stays outside the adapters

These parts of the CLI still name agents, on purpose:

- Defaults: `claude-code` for `--agent` of `init`, `fork`, `resume` and
  `review`, and `claude-code,codex` for `trace setup` without `--agent`.
  These are product decisions, not agent knowledge.
- `projectRemovalOrder` (see step 3).
- Some engine error messages that are only produced when SQLite fails still
  say "Claude transcript offset" or "Antigravity". They were kept
  byte-identical when the transcript handling moved into the adapters.
