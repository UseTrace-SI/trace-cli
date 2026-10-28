# AGENTS.md — Trace

Trace is a local CLI and daemon that saves a keyframe at each coding-agent turn. Users can
inspect history, rewind, fork, resume, and ask why a line exists. This public repository
contains only the CLI; the companion Cloudflare app lives in a separate private `trace-app`
repository. Website: usetrace.si. Module: `github.com/UseTrace-SI/trace-cli`.

Work phase by phase from `IMPLEMENTATION_PLAN.md`. Do only the phase you were asked to do.

## Commands

```sh
make build      # pure-Go binary at bin/trace
make test       # go test -race ./...
make lint       # go vet ./... && staticcheck ./...
make e2e        # ./scripts/e2e.sh
make test-sync  # sync client tests
```

Every phase must end with `make lint test` passing.

## Stack and constraints

- Go 1.23+. One binary: `trace` (CLI) and `trace daemon` (background process).
- SQLite via `modernc.org/sqlite` (pure Go). **No cgo** in the default build.
  cgo is allowed only behind the `treesitter` build tag (Phase 13).
- Direct dependencies are pinned in `go.mod`, including Cobra, fsnotify, modernc SQLite,
  zstd, the MCP Go SDK, `x/crypto`, and optional Tree-sitter grammars. Ask before adding
  another direct dependency.
- Shell out to the `git` binary for git operations (worktrees, rev-parse). Do not use go-git for writes.

## Hard rules

1. **Never block the agent.** Hook commands (`trace hook ...`) must finish in < 20 ms p95:
   write one JSON line to the daemon socket (or spool file) and exit 0. A hook must never exit
   non-zero or print to stdout in a way that changes agent behaviour.
2. **The Trace runtime never modifies git history.** No commits, branches, notes, or trailers.
   Its only Git writes are installing/chaining a `post-commit` hook and `git worktree add`
   in `fork`.
3. **Never store secrets.** Text (prompts, reasoning, tool calls) passes through `internal/redact`
   before any DB write. Files matching the secret path excludes are never read or stored.
4. **Local by default.** The daemon listens only on a Unix socket with mode 0600. Network
   calls are limited to an explicit update check and user-initiated GitHub login or sync.
5. **History and daemon data stay under `.trace/`**, which `trace init` adds to
   `.git/info/exclude`. Agent configuration and Git hook files are merged in place.
6. **Rewind is always undoable.** Take a safety keyframe before any destructive restore.
7. Merge, never overwrite, user config files (`.claude/settings.local.json`, `.mcp.json`, git hooks).

## Code conventions

- Packages live under `internal/`. Public surface is the CLI only.
- Errors: wrap with `fmt.Errorf("doing x: %w", err)`. No panics outside `main`.
- IDs: ULID strings. Timestamps: Unix milliseconds (int64) in the DB.
- Every package has table-driven unit tests. Recorded agent payloads live in `testdata/`.
- Commands that print user-facing reports support `--json` for machine-readable output.
- Keep functions small; prefer the standard library.
- Log with `log/slog` to `.trace/daemon.log`; never log redacted-out values.

## Definition of done (every phase)

- Acceptance criteria in the phase are met and covered by tests.
- `make lint test` passes on macOS and Linux CI.
- README "Status" section updated with what now works.
- No TODOs left without an issue reference.
