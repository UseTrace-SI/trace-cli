# AGENTS.md — Trace

Trace is a local CLI + daemon that saves a checkpoint ("keyframe") with full context at every
coding-agent turn, so users can rewind, fork, resume, and ask why any line exists.
Website: usetrace.si. Module: `github.com/UseTrace-SI/trace-cli`.

Work phase by phase from `IMPLEMENTATION_PLAN.md`. Do only the phase you were asked to do.

## Commands

```
make build      # go build -o bin/trace ./cmd/trace
make test       # go test -race ./...
make lint       # go vet ./... && staticcheck ./...
make e2e        # ./scripts/e2e.sh (added in Phase 4)
```

Every phase must end with `make lint test` passing.

## Stack and constraints

- Go 1.23+. One binary: `trace` (CLI) and `trace daemon` (background process).
- SQLite via `modernc.org/sqlite` (pure Go). **No cgo** in the default build.
  cgo is allowed only behind the `treesitter` build tag (Phase 13).
- Allowed third-party deps: `github.com/spf13/cobra`, `github.com/fsnotify/fsnotify`,
  `modernc.org/sqlite`, `github.com/klauspost/compress/zstd`, `github.com/oklog/ulid/v2`,
  `github.com/sergi/go-diff` (or an equivalent Myers diff), an MCP Go SDK (Phase 9).
  Ask before adding anything else.
- Shell out to the `git` binary for git operations (worktrees, rev-parse). Do not use go-git for writes.

## Hard rules

1. **Never block the agent.** Hook commands (`trace hook ...`) must finish in < 20 ms p95:
   write one JSON line to the daemon socket (or spool file) and exit 0. A hook must never exit
   non-zero or print to stdout in a way that changes agent behaviour.
2. **Never modify git history.** No commits, no branches, no notes, no trailers. The only git
   writes allowed: installing/chaining a `post-commit` hook, and `git worktree add` in `fork`.
3. **Never store secrets.** Text (prompts, reasoning, tool calls) passes through `internal/redact`
   before any DB write. Files matching the secret path excludes are never read or stored.
4. **Local only.** No network calls in the CLI or daemon (except the opt-in update check in Phase 10).
   The daemon listens only on a Unix socket with mode 0600.
5. **Everything under `.trace/`**, which `trace init` adds to `.git/info/exclude`.
6. **Rewind is always undoable.** Take a safety keyframe before any destructive restore.
7. Merge, never overwrite, user config files (`.claude/settings.local.json`, `.mcp.json`, git hooks).

## Code conventions

- Packages live under `internal/`. Public surface is the CLI only.
- Errors: wrap with `fmt.Errorf("doing x: %w", err)`. No panics outside `main`.
- IDs: ULID strings. Timestamps: Unix milliseconds (int64) in the DB.
- Every package has table-driven unit tests. Recorded agent payloads live in `testdata/`.
- All CLI commands support `--json` for machine-readable output.
- Keep functions small; prefer the standard library.
- Log with `log/slog` to `.trace/daemon.log`; never log redacted-out values.

## Definition of done (every phase)

- Acceptance criteria in the phase are met and covered by tests.
- `make lint test` passes on macOS and Linux CI.
- README "Status" section updated with what now works.
- No TODOs left without an issue reference.
