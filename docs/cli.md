# CLI reference

All commands accept `--repo PATH` and `--json` where they produce user-facing output.

| Command | Purpose |
|---|---|
| `trace version` | Print build version, commit, and date |
| `trace init [--agent claude-code\|codex\|cursor\|agy]` | Set up local history and an agent adapter |
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
| `trace daemon stop` | Stop the local daemon |
| `trace mcp` | Serve read-only MCP tools over stdio |

The `--json` output uses the fields shown by each command and is intended for scripts. Step numbers are numeric and remain stable even when retention removes an older step.
