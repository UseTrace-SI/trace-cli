# Trace

Trace keeps local keyframes of coding-agent turns so a project can be inspected, rewound, and forked.

## Status

The CLI records Claude Code, Codex, Cursor Agent, and Antigravity CLI (`agy`) turns, stores compressed keyframes, and supports log, show, rewind, status, why, fork, resume, garbage collection, read-only MCP history tools, and Agent Trace export. It captures available assistant text, tool names, and read paths from agent hooks or transcripts. Optional Tree-sitter builds classify structural edits. An opt-in [Pro/Team sync client](docs/sync.md) uploads history and can recover it into a fresh Git checkout through the separate Trace app, which is not yet deployed. `make test-sync` checks the client. See [quickstart](docs/quickstart.md) and [privacy](docs/privacy.md).

## Repository layout

| Path | Purpose |
| --- | --- |
| `cmd/trace`, `internal/` | Go CLI, daemon, and local storage |
| `docs/` | CLI, sync, privacy, and release guides |
| `scripts/` | Install, end-to-end, performance, and Homebrew tooling |

The Go module and project Makefile live at the repository root. Run `make lint test` for the CLI and `make test-sync` for its sync client. When the private `trace-app` repository is checked out beside this one, `make test-sync-integration` exercises both sides of the protocol.
