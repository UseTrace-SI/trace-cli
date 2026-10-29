# Trace — Implementation Plan (phase by phase)

Read [AGENTS.md](AGENTS.md) first. This plan records the CLI's original phase scope and
acceptance criteria. The companion Cloudflare application is maintained in a separate private
`trace-platform` repository. For current user-facing behavior, see the [README](README.md) and
[CLI reference](docs/cli.md).

Implement only a requested phase. Each phase lists its dependencies and ends with a mergeable
change, passing `make lint test`, and tests for its acceptance criteria.

| Phase | Name | Original week | Depends on |
| --- | --- | --- | --- |
| 0 | Scaffold | 1 | — |
| 1 | Storage: SQLite + blobs + manifests | 1 | 0 |
| 2 | Events, daemon, socket, spool | 1–2 | 1 |
| 3 | Claude Code adapter + `init` + keyframes | 2 | 2 |
| 4 | `log`, `show`, `rewind`, `status` + e2e harness | 2 | 3 |
| 5 | File watcher, ops/deltas, git hook | 3 | 4 |
| 6 | Redaction | 3 | 2 |
| 7 | `why` + `fork` | 4 | 5 |
| 8 | Codex adapter + `resume` | 5 | 7 |
| 9 | MCP server + full-text search | 6 | 7 |
| 10 | Packaging, release, docs | 6 | 9 |
| 11 | `gc`, retention, performance | 8 | 10 |
| 12 | Cursor Agent CLI + Antigravity CLI (`agy`) adapters | 9 | 8 |
| 13 | tree-sitter structural ops + Agent Trace export (v1) | 10 | 11 |
| 14 | Sync client (Pro/Team) — client side only | 11–12 | 13 |

---

## Phase 0 — Scaffold

**Goal:** an empty but buildable, tested, linted Go project.

**Create**
```
go.mod                     module github.com/UseTrace-SI/trace-cli, go 1.23
cmd/trace/main.go          calls cli.Execute()
internal/cli/root.go       cobra root: `trace`, global flags --json, --repo (default: git toplevel of cwd)
internal/cli/version.go    `trace version` prints version, commit, date (ldflags)
internal/repo/repo.go      FindRoot(cwd) (string, error) via `git rev-parse --show-toplevel`;
                           TraceDir(root) = root/.trace
Makefile                   build, test, lint, e2e targets
.github/workflows/ci.yml   macos-latest + ubuntu-latest: go vet, staticcheck, go test -race
README.md                  one paragraph + Status section
LICENSE                    Apache-2.0
```

**Acceptance**
- `make build && ./bin/trace version` prints a version.
- `trace --repo /not/a/repo version` still works; repo-requiring commands (later) error with
  "not inside a git repository".
- CI green on both OSes.

**Prompt for Codex**
> Implement Phase 0 from IMPLEMENTATION_PLAN.md following AGENTS.md. Create only the files listed,
> make CI green, and stop.

---

## Phase 1 — Storage: SQLite, blob store, manifests

**Goal:** durable storage for everything Trace records.

**Create**
```
internal/store/store.go       Open(path) (*DB, error) — WAL, foreign_keys=on, busy_timeout=5000
internal/store/migrations.go  embedded, versioned SQL migrations (schema_version table)
internal/store/models.go      Session, Turn, Keyframe, Op, Commit structs + CRUD methods
internal/blob/blob.go         content-addressed store
internal/manifest/manifest.go tree snapshots
```

**Schema (migration 001)**
```sql
CREATE TABLE sessions (id TEXT PRIMARY KEY, agent TEXT NOT NULL, agent_sid TEXT,
  worktree TEXT NOT NULL, started_at INTEGER NOT NULL, ended_at INTEGER);
CREATE TABLE turns (id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id),
  seq INTEGER NOT NULL, prompt TEXT, reasoning TEXT, tool_calls TEXT, files_read TEXT,
  started_at INTEGER NOT NULL, ended_at INTEGER);
CREATE UNIQUE INDEX turns_seq ON turns(seq);
CREATE TABLE keyframes (id TEXT PRIMARY KEY, turn_id TEXT REFERENCES turns(id),
  seq INTEGER NOT NULL UNIQUE, manifest_hash TEXT NOT NULL, git_head TEXT,
  tag TEXT, created_at INTEGER NOT NULL);           -- tag: baseline | turn | pre-rewind
CREATE TABLE ops (id TEXT PRIMARY KEY, turn_id TEXT REFERENCES turns(id), session_id TEXT,
  path TEXT NOT NULL, kind TEXT NOT NULL, start_line INTEGER, end_line INTEGER,
  before_hash TEXT, after_hash TEXT, source TEXT NOT NULL, at INTEGER NOT NULL);
CREATE INDEX ops_path ON ops(path, at);
CREATE TABLE commits (sha TEXT PRIMARY KEY, keyframe_id TEXT REFERENCES keyframes(id),
  at INTEGER NOT NULL);
```
Note: keyframes carry their own `seq` (the user-facing step number). Step 0 is the baseline.

**Blob store API**
```go
type Hash string                                  // hex sha256 of the *uncompressed* bytes
func Open(dir string) (*Store, error)             // dir = .trace/objects
func (s *Store) Put(r io.Reader) (Hash, error)    // zstd-compress, write tmp then rename, idempotent
func (s *Store) PutBytes(b []byte) (Hash, error)
func (s *Store) Get(h Hash) (io.ReadCloser, error)
func (s *Store) Has(h Hash) bool
// layout: objects/<h[0:2]>/<h[2:]>
```

**Manifest API**
```go
type Entry struct { Hash blob.Hash; Mode fs.FileMode }
type Manifest map[string]Entry                    // key = slash path relative to repo root
func Scan(root string, ig Ignorer, bs *blob.Store) (Manifest, error)  // full scan, stores blobs
func Update(prev Manifest, root string, dirty []string, ig Ignorer, bs *blob.Store) (Manifest, error)
func (m Manifest) Save(bs *blob.Store) (blob.Hash, error)  // canonical sorted JSON
func Load(h blob.Hash, bs *blob.Store) (Manifest, error)
func Diff(a, b Manifest) (added, modified, deleted []string)
type Ignorer interface { Ignored(rel string, isDir bool) bool }
```
Implement `Ignorer` honouring `.gitignore` (use `git check-ignore --stdin` in batch, or a gitignore
matcher), plus always-ignore: `.git/`, `.trace/`, and the secret path excludes from Phase 6
(`.env*`, `*.pem`, `*.key`, `id_rsa*`, `.aws/`, `.ssh/`) — define that list in
`internal/manifest/excludes.go` now; Phase 6 will import it.

**Acceptance**
- Put/Get round-trips bytes exactly; putting the same content twice stores one object.
- Scan of a temp repo produces a manifest; Update after editing 2 files changes exactly 2 entries.
- Save/Load round-trip; identical manifests produce identical hashes.
- Migrations are idempotent (open twice = no error).

**Prompt for Codex**
> Implement Phase 1 from IMPLEMENTATION_PLAN.md. Match the APIs exactly, add table-driven tests for
> every acceptance criterion, and stop.

---

## Phase 2 — Events, daemon, socket, spool

**Goal:** a background daemon that receives events from short-lived hook processes without ever
making them wait.

**Create**
```
internal/event/event.go     normalized event schema
internal/daemon/server.go   Unix socket server at .trace/sock (mode 0600), newline-delimited JSON
internal/daemon/client.go   Send(ev) — connect with 50 ms timeout, write, close; on failure append to spool
internal/daemon/spool.go    .trace/spool.jsonl append (O_APPEND, flock) and ingest-on-start
internal/daemon/lifecycle.go EnsureRunning(): start `trace daemon` detached if socket is dead;
                            pidfile .trace/daemon.pid; idle shutdown after 30 min without events
internal/cli/daemon.go      `trace daemon` (foreground, hidden), `trace daemon stop`
internal/cli/hook.go        `trace hook <agent> <event>` — reads stdin, builds Event, Send(), exit 0 always
```

**Event schema**
```go
type Kind string
const (SessionStart Kind = "session_start"; TurnStart = "turn_start"; ToolEdit = "tool_edit"
       TurnEnd = "turn_end"; SessionEnd = "session_end"; GitCommit = "git_commit")
type Event struct {
  Kind           Kind              `json:"kind"`
  Agent          string            `json:"agent"`            // claude-code | codex | cursor | agy | git
  AgentSessionID string            `json:"agent_session_id,omitempty"`
  Worktree       string            `json:"worktree"`
  At             int64             `json:"at"`               // unix ms
  Prompt         string            `json:"prompt,omitempty"`
  ToolName       string            `json:"tool_name,omitempty"`
  Paths          []string          `json:"paths,omitempty"`
  Lines          *LineRange        `json:"lines,omitempty"`
  TranscriptPath string            `json:"transcript_path,omitempty"`
  GitSHA         string            `json:"git_sha,omitempty"`
  Raw            json.RawMessage   `json:"raw,omitempty"`    // original payload, never persisted unredacted
}
```
The daemon processes events through a single ordered queue (one goroutine) and hands them to an
`engine.Engine` interface (stub in this phase: logs events).

**Acceptance**
- Hook → daemon round trip measured in a test: p95 < 20 ms over 200 sends.
- Kill the daemon, send 10 events: they land in the spool; start the daemon: all 10 are processed
  in order and the spool is truncated.
- `trace hook` with garbage stdin exits 0 and logs a warning.
- Two hooks sending concurrently never interleave lines.

**Prompt for Codex**
> Implement Phase 2 from IMPLEMENTATION_PLAN.md. The hook path must never fail or block; prove the
> latency and spool criteria with tests, and stop.

---

## Phase 3 — Claude Code adapter, `trace init`, keyframes

**Goal:** real capture: every Claude Code turn produces a keyframe.

**Before coding:** read the current Claude Code hooks documentation and confirm event names and
payload fields. Record 3 real payloads per event into `testdata/claudecode/` and write the parser
against those fixtures. Expected (verify): common fields `session_id`, `transcript_path`, `cwd`,
`hook_event_name`; `UserPromptSubmit` has `prompt`; `PostToolUse` has `tool_name`, `tool_input`,
`tool_response`.

**Create**
```
internal/adapters/claudecode/parse.go    payload -> []event.Event
internal/adapters/claudecode/install.go  merge hooks into .claude/settings.local.json; remove on uninstall
internal/adapters/claudecode/transcript.go read new lines of transcript_path since last offset
                                         (store offset per session) -> reasoning text, files read
internal/engine/engine.go                real engine: sessions, turns, keyframes
internal/cli/init.go                     `trace init [--agent claude-code]`
internal/cli/uninstall.go                `trace uninstall [--purge]`
```

**Hook mapping**

| Claude Code event | Trace event | Notes |
|---|---|---|
| SessionStart | SessionStart | create session row |
| UserPromptSubmit | TurnStart | open turn, `seq` assigned at TurnEnd |
| PostToolUse (Edit, Write, MultiEdit, NotebookEdit) | ToolEdit | path from `tool_input.file_path` |
| PostToolUse (Bash) | ToolEdit with no paths | mark "unknown dirty"; Phase 5 watcher resolves |
| Stop | TurnEnd | build keyframe |
| SubagentStop | (log only for now) | |

Installed hook commands: `trace hook claude-code <event>`. Matcher for PostToolUse:
`Edit|Write|MultiEdit|NotebookEdit|Bash`.

**Engine at TurnEnd**
1. dirty = paths from this turn's ToolEdits; if any "unknown dirty", do a full `manifest.Scan`.
2. `manifest.Update(prev, root, dirty)` → Save → insert keyframe (`tag=turn`, next `seq`) and
   finalize the turn row (prompt, reasoning, tool_calls JSON, files_read) in **one transaction**.
3. Text fields pass through a `Redactor` interface (no-op in this phase; Phase 6 implements it).

**`trace init`**
- Creates `.trace/`, DB, objects dir; appends `.trace/` to `.git/info/exclude` (once).
- Installs Claude Code hooks (merge, preserve existing user hooks, idempotent).
- Takes baseline keyframe `seq=0`, `tag=baseline`.
- Prints next steps.

**Acceptance**
- Replaying fixture payloads for a 3-turn session yields 3 turns and keyframes 1–3 (+ baseline 0).
- Running `init` twice does not duplicate hooks; `uninstall` restores the original settings file
  byte for byte when Trace added the only entries.
- Existing unrelated hooks in settings.local.json survive install and uninstall.

**Prompt for Codex**
> Implement Phase 3 from IMPLEMENTATION_PLAN.md. First verify Claude Code hook payloads against the
> official docs and save fixtures in testdata/claudecode/; build the parser against the fixtures.
> Keep install/uninstall non-destructive. Stop after acceptance tests pass.

---

## Phase 4 — `log`, `show`, `rewind`, `status` + e2e harness

**Create**
```
internal/cli/log.go       steps table: seq, time, agent, prompt (first 60 chars), files changed count
internal/cli/show.go      `show <step>`: prompt, reasoning, tool calls, unified diff vs previous keyframe
internal/cli/rewind.go    `rewind <step> [--files glob] [--dry-run]`
internal/cli/status.go    daemon up?, active sessions, latest step, .trace size
internal/restore/restore.go  Apply(target Manifest, root, ig, bs) (changed []string, err)
scripts/e2e.sh            fake-agent harness (see below)
scripts/fake-agent/       Go program that emits Claude-style hook payloads and edits files
```

**Rewind algorithm**
1. Take safety keyframe of current state (`tag=pre-rewind`, new seq).
2. target = Load(keyframe[step].manifest_hash).
3. current = Scan. For each path: write blob if hash differs (write tmp + rename, restore mode);
   delete files present in current but absent in target (never ignored/excluded paths, never `.git/`);
   create missing dirs; remove now-empty dirs that Trace created.
4. Record a `rewind` op row; print changed files and "undo with: trace rewind <safety seq>".
5. `--files` limits steps 3 to matching paths. `--dry-run` prints the plan only.

**E2E harness:** creates a temp git repo, runs `trace init`, runs fake-agent for 5 turns
(edits, creates, deletes), then asserts: `trace log` shows 5 steps; `rewind 2` makes the tree hash
equal to step 2's manifest; `rewind <safety>` restores step 5 exactly.

**Acceptance**
- E2E passes on macOS and Linux CI.
- `rewind` of 1,000 changed files < 1 s (benchmark test, skipped with -short).
- `--json` output for all four commands is stable and documented in `docs/cli.md`.

**Prompt for Codex**
> Implement Phase 4 from IMPLEMENTATION_PLAN.md including the fake-agent e2e harness. Rewind must be
> byte-exact and undoable. Stop after e2e and benchmarks pass.

---

## Phase 5 — File watcher, ops/deltas, git hook

**Create**
```
internal/watch/watch.go     fsnotify recursive watcher, 150 ms debounce, Ignorer, hashes into blobs
internal/engine/ops.go      ops rows with line ranges (Myers diff vs previous blob)
internal/gitx/hook.go       install/chain post-commit hook: `trace hook git commit`
internal/gitx/git.go        Head(), RevParse(), WorktreeAdd() helpers (shell out)
```

**Rules**
- Changes inside an open turn → ops with that `turn_id`, `source=watcher` (or `source=tool` for
  ToolEdit paths). Changes with no open turn → ops with `turn_id NULL` (human edit).
- Watcher also resolves "unknown dirty" from Bash tool calls.
- Two sessions open in one worktree: tag ops by `session_id`; if both touch the same path within
  2 s, mark later op `kind=contested` (show in `trace show`).
- post-commit: link commit SHA to the latest keyframe; if an existing post-commit hook exists,
  rename to `post-commit.pre-trace` and call it first.

**Acceptance**
- Edit a file manually between turns → op with NULL turn_id and correct line range.
- Bash tool that writes 3 files → those 3 paths in the turn's keyframe without a full scan
  (assert scan not called).
- Commit after a turn → `commits` row linked to that keyframe; existing user hook still runs.

**Prompt for Codex**
> Implement Phase 5 from IMPLEMENTATION_PLAN.md. Never write into commits; chain any existing git
> hook. Stop after acceptance tests pass.

---

## Phase 6 — Redaction

**Create** `internal/redact/` implementing the `Redactor` interface used by the engine.

**Rules (in order)**
1. Path excludes (shared list from Phase 1): never read or store.
2. Known formats: AWS access keys, GitHub tokens (ghp_, gho_, github_pat_), Slack tokens, Stripe
   keys, OpenAI/Anthropic-style keys (sk-...), JWTs, PEM private key blocks, URLs with passwords.
3. Entropy: tokens ≥ 20 chars, Shannon entropy > 4.0 bits/char, within 30 chars of
   key|secret|token|password|passwd|api.
4. Env values: values of env vars whose names match `(KEY|SECRET|TOKEN|PASSWORD)` are masked.
5. PII (only if config `redact.pii = true`): emails, phone numbers.

Replacement: `⟨redacted:<kind>:<first 8 hex of sha256(value)>⟩`.
Blob file contents are **not** redacted (rewind needs exact bytes) — they are protected by rule 1.

**Tests**
- `testdata/redact/positives/*` (fake secrets of each kind) and `negatives/*` (git SHAs, UUIDs,
  base64 images, lockfile hashes).
- CI check: after the e2e run with fixture secrets in prompts, `grep` of `trace.db` for each
  fixture secret finds nothing.

**Prompt for Codex**
> Implement Phase 6 from IMPLEMENTATION_PLAN.md. Prefer false positives over leaks, but keep the
> negatives corpus passing. Stop after the CI leak check passes.

---

## Phase 7 — `why` and `fork`

**`trace why <file>:<line> [--all]`**
1. Read current file; walk `ops` for that path newest first.
2. Map the line back through each op's ranges (adjust for inserted/deleted lines) until an op's
   after-range covers it.
3. Output: step seq, agent, time, prompt, reasoning excerpt (≤ 400 chars), linked commit.
   Human op → "edited by you (between steps N and N+1)". `--all` lists every op that touched it.

**`trace fork <step> [--agent X] [--name N] [--no-launch]`**
1. `git worktree add ../<repo>-fork-<step>[-name] <git_head of step>`.
2. Apply the step's manifest on top (restore.Apply) so uncommitted agent work is included.
3. Write `.trace/context.md` in the fork: original task prompt, last 5 turns summarised
   (prompt + files changed + reasoning excerpt), the step's diff stat.
4. Launch agent unless `--no-launch`: `claude "$(cat .trace/context.md)"` or `codex "..."`
   (command templates configurable in `.trace/config.toml`).
5. The fork's daemon records `forked_from = <session>:<step>` (add column via migration 002).

**Acceptance**
- `why` returns the correct step for a line edited in step 2 then shifted by inserts in step 4.
- `fork 3 --no-launch` creates a worktree whose tree equals keyframe 3 exactly.
- Property test: random edit sequences, `why` never points at a step that didn't touch the line.

**Prompt for Codex**
> Implement Phase 7 from IMPLEMENTATION_PLAN.md with property tests for why-line mapping. Stop after
> acceptance tests pass.

---

## Phase 8 — Codex adapter + `resume`

**Before coding:** inspect real Codex rollout JSONL files under `~/.codex/sessions/` and record
anonymised fixtures into `testdata/codex/`. If Codex now provides an official hook mechanism,
prefer it and note that in the PR.

**Create**
```
internal/adapters/codex/tail.go    watch ~/.codex/sessions for files whose cwd == repo root;
                                   user message -> TurnStart, file-change tool calls -> ToolEdit,
                                   end of assistant response -> TurnEnd
internal/cli/resume.go             `resume <step> [--agent X]`
```

**`resume`**: rewind current worktree to step (with safety keyframe), write `.trace/context.md`,
launch the chosen agent. Purpose: continue a Claude Code step in Codex and vice versa.

**Acceptance**
- Fixture replay yields correct turns/keyframes for a Codex session.
- E2E: Claude-style session 3 turns → `resume 2 --agent codex --no-launch` → tree equals step 2,
  context.md contains step 1–2 prompts.

**Prompt for Codex**
> Implement Phase 8 from IMPLEMENTATION_PLAN.md. Build the Codex parser from recorded fixtures, not
> assumptions. Stop after acceptance tests pass.

---

## Phase 9 — MCP server + full-text search

**Create** `internal/mcp/` (use an MCP Go SDK; stdio transport) and `trace mcp` command.
Migration 003: FTS5 virtual table over turns(prompt, reasoning) kept in sync by triggers.

| Tool | Input | Output |
|---|---|---|
| list_checkpoints | limit, session?, path? | steps: seq, agent, prompt summary, files changed |
| get_turn | step | prompt, reasoning, tool calls, diff stat |
| get_context | path | steps that touched the file with why-excerpts, newest first |
| why_line | path, line | same as `trace why` |
| search_history | query, limit | matching steps (FTS5) |

All tools are read-only. Cap responses at ~4,000 tokens with a `cursor` for more.
`trace init` registers the server in `.mcp.json` (merge, don't overwrite).

**Acceptance**
- MCP conformance test with a scripted client calling each tool.
- Responses never contain fixture secrets (reuse Phase 6 leak check).

**Prompt for Codex**
> Implement Phase 9 from IMPLEMENTATION_PLAN.md. Read-only tools only. Stop after the scripted
> client test passes.

---

## Phase 10 — Packaging, release, docs

**Create**
```
.goreleaser.yaml      darwin/linux × amd64/arm64, checksums, signed (cosign keyless)
scripts/install.sh    detects OS/arch, downloads from GitHub Releases, verifies checksum
Homebrew tap          usetrace/homebrew-tap, formula `trace`
docs/                 quickstart, commands (generated from cobra), privacy & redaction, how it works
```
- Opt-in update check (once/day) — the only network call; disabled by `TRACE_NO_UPDATE_CHECK=1`.
- Short alias `trc` installed by the formula (do not use `tr`, it is a standard Unix command).

**Acceptance**
- `goreleaser release --snapshot` builds all targets in CI.
- Fresh macOS and Ubuntu VMs: install.sh → `trace init` → e2e passes.

---

## Phase 11 — `gc`, retention, performance

- `trace gc [--keep-days N]`: full scan keyframe; drop keyframes older than N (never baseline,
  never ones linked to commits); delete unreferenced blobs (mark-and-sweep over manifests).
- Config `retention.days` (default 30 for free).
- Periodic full scan every 50 keyframes to catch missed watcher events.
- Benchmarks in CI, fail on > 20% regression:
  hook round trip < 20 ms p95 · keyframe (100 changed files, 10k-file repo) < 200 ms ·
  rewind 1,000 files < 1 s · log/why on 5,000 steps < 100 ms · daemon idle RSS < 80 MB.

---

## Phase 12 — Cursor Agent CLI + Antigravity CLI (`agy`) adapters

Same pattern as Phases 3 and 8: verify each agent's current hook/session mechanism from its docs,
record fixtures, map to the 6 Trace events, add install/uninstall, add fixture replay + e2e tests.
Antigravity CLI replaces the discontinued Gemini CLI integration. Its workspace hooks live in
`.agents/hooks.json` and its executable is `agy`. Keep uninstall support for older Gemini hooks.

---

## Phase 13 — v1: structural ops + Agent Trace export

- `internal/structure/` behind build tag `treesitter` (cgo): for Go, TypeScript, Python, Rust,
  classify ops as function added/removed/renamed/changed; store in `ops.kind` detail column.
  `trace show` and `why` print symbol names when available.
- `trace export --format agent-trace --out <file>`: emit Cursor's Agent Trace format from ops
  (contributor = ai/human, model/agent, line ranges, conversation reference). Validate against the
  published spec's examples.
- Tag v1.0.0.

---

## Phase 14 — Sync client (Pro / Team)

Phase 14 covers the client. The companion Worker is maintained in the private `trace-platform` repository.

- `trace login` (GitHub OAuth device flow), `trace sync [--watch]`, and `trace sync pull --into PATH`.
- Protocol: append-only. POST rows newer than last-synced ULID; blobs by hash with batched
  `have/need` (500 hashes per request).
- Pro: client-side encryption, XChaCha20-Poly1305, key derived on device and never uploaded.
- Team: TLS + server-side encryption; shares with team members who have repo access.
- Sync is off by default and never runs without `trace login`.

---

## How to hand this to Codex

1. Put `AGENTS.md` and `IMPLEMENTATION_PLAN.md` in the repo root.
2. One phase per Codex task/branch. Use the phase's prompt, e.g.:
   > Implement Phase 3 from IMPLEMENTATION_PLAN.md following AGENTS.md. Do not start other phases.
3. Review the PR against the phase's acceptance list before merging; then start the next phase.
4. If a phase is too big for one run, ask Codex to split it into sub-PRs (e.g. 4a commands, 4b e2e)
   but keep the acceptance criteria unchanged.
