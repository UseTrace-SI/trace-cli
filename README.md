# Trace

Trace keeps local keyframes of coding-agent turns so a project can be inspected, rewound, and forked.

## Status

The CLI records Claude Code, Codex, Cursor Agent, and Antigravity CLI (`agy`) turns, stores compressed keyframes, and supports log, show, rewind, status, why, fork, resume, garbage collection, read-only MCP history tools, and Agent Trace export. It captures available assistant text, tool names, and read paths from agent hooks or transcripts. Optional Tree-sitter builds classify structural edits. An opt-in [Pro/Team sync client](docs/sync.md) uploads history in Worker-sized batches and can recover it into a fresh Git checkout through the separate Trace app, which is not yet deployed. `make test-sync` checks the client. See [quickstart](docs/quickstart.md) and [privacy](docs/privacy.md).

## Build and try it

Go 1.23 or newer and Git are required. Until the first tagged release, build from source:

```sh
git clone https://github.com/UseTrace-SI/trace-cli.git
cd trace-cli
make build
export PATH="$PWD/bin:$PATH"
cd /path/to/your/git/repo
trace init --agent codex
```

Choose `claude-code`, `cursor`, or `agy` instead of `codex` to install that agent's hooks. After an agent turn, run `trace log`, `trace show 1`, or `trace why path/to/file.go:12`. Preview a restore with `trace rewind 1 --dry-run`. See the [quickstart](docs/quickstart.md) for fork and resume.

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

The Go module and project Makefile live at the repository root. Run `make lint test` for the CLI and `make test-sync` for its sync client. When the private `trace-app` repository is checked out beside this one, `make test-sync-integration` exercises both sides of the protocol.

Trace is licensed under [Apache 2.0](LICENSE).
