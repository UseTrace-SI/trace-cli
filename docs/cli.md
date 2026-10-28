# CLI reference

Run `trace --help` or `trace <command> --help` for the installed version's full flag list. The global `--repo PATH` flag selects a repository; `--json` requests machine-readable output where a command supports it. Step arguments are the numbers shown by `trace log`.

## Commands

| Command | Purpose |
| --- | --- |
| `trace version` | Print build version, commit, and date |
| `trace init [--agent NAME]` | Set up local history and an agent adapter |
| `trace uninstall [--purge]` | Remove integrations, optionally deleting history |
| `trace log` | List keyframes |
| `trace show STEP` | Show a turn and file diff |
| `trace rewind STEP [--files GLOB] [--dry-run]` | Restore a snapshot with an undo step |
| `trace why FILE:LINE [--all]` | Find the change responsible for a line |
| `trace fork STEP [--agent NAME] [--name SUFFIX] [--no-launch]` | Create a worktree at a step |
| `trace resume STEP [--agent NAME] [--no-launch]` | Rewind and continue with an agent |
| `trace status` | Show daemon, session, and storage status |
| `trace gc [--keep-days N] [--dry-run]` | Remove expired history and unused blobs |
| `trace update-check` | Check for a release, at most once per day |
| `trace export --format agent-trace --out FILE` | Export line attribution as Agent Trace JSONL |
| `trace login --remote OWNER/REPO --mode pro\|team --endpoint HTTPS_URL` | Authorize optional sync with GitHub device flow |
| `trace sync [--watch]` | Upload local history to the configured sync server |
| `trace sync pull --into PATH` | Recover synced history into a fresh Git checkout |
| `trace daemon start` | Start the local daemon if it is not running |
| `trace daemon stop` | Stop the local daemon |
| `trace mcp` | Serve read-only MCP tools over stdio |

Agent names are `claude-code`, `codex`, `cursor`, and `agy`. Re-run `trace init` to enable another adapter in the same repository.

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

Sync is off until login and an explicit sync command. See the [sync guide](sync.md) for passphrases, access control, and recovery.
