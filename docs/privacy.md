# Privacy and redaction

## Local history and network access

Trace stores data locally in `.trace/` by default. It contacts the sync server for explicit login/connection or manual sync, and for background uploads after you approve computer login and enable Trace, opt into legacy `trace login --mode pro --auto-sync`, or approve `trace connect`. Automatic sync sends existing and new history from the local daemon in the background; stop it with `trace sync disable-auto`. Pro sync encrypts rows and blobs on the client, with its derived key held in the OS credential store for unattended sync. Team sync sends rows and blobs over HTTPS for server-side encryption. See [sync](sync.md) for the protocol and setup.

An approved personal connection uploads history and bounded derived Brain/Graph
snapshots to your hosted account. Payloads are encrypted on the server and readable
in your signed-in dashboard; this mode is not end-to-end encrypted. Tokens stay
in the OS credential store, expire, and can be revoked. `trace connect` grants
access to one project; `trace login` explicitly approves this computer for all
traced projects in the signed-in account. First computer login starts paused
until `trace enable`. `trace disable` pauses recording and automatic uploads
without deleting history or credentials. Per-agent pause stops that integration
from recording; previously captured history can still sync.
Manual memories and raw companion SQLite files are excluded from derived uploads.
Revoking a device does not delete uploaded history.

## Text redaction

Prompt text is redacted before it reaches the event spool or SQLite. Trace masks common API keys, tokens, private-key blocks, credentialed URLs, high-entropy values near secret labels, and environment variable values with secret-like names. Optional PII masking covers email addresses and phone numbers.

For Claude Code and Antigravity CLI, the daemon reads newly appended assistant transcript records after a turn ends and redacts text before storing it. Tool call records keep names and safe repository paths; they do not store raw tool inputs or tool results.

Codex assistant output and reasoning summaries, Cursor Agent CLI response and thought hooks, Windsurf Cascade response summaries, and Gemini CLI and Qwen Code final responses pass through the same redactor before storage. Copilot CLI hooks record prompts, tool names, and safe paths without raw tool arguments or results. Windsurf's full transcript file is not read by the hook. The available context depends on what each agent actually emits.

## File exclusions and removal

Trace does not read `.env*`, `*.pem`, `*.key`, `id_rsa*`, `.aws/`, or `.ssh/`. File blobs for other paths are kept byte-exact so rewind can restore them. Do not put credentials in ordinary source files if you do not want those bytes in local snapshots.

`trace uninstall` removes integrations while keeping history. `trace uninstall --purge` removes `.trace/` data as well.

## Preview and project exclusions

`trace connect --preview --json` and `trace sync --dry-run --json` run locally,
without provider login or upload. They report history scope and paths with
potential credentials; they never print matched credential values. Scanning
samples up to 1 MiB per file version and 20 MiB overall and reports incomplete
coverage. Detection is best effort, not a guarantee that source contains no secrets.

A project-root `.traceignore` excludes paths from future CLI snapshots and Graph
indexes, even when tracked by Git. Use a relative filename, a directory prefix
ending in `/`, or a single-level glob such as `secrets/*.json` or `*.local`.
Blank lines and `#` comments are supported. Negation and `**` patterns are rejected.
Built-in secret exclusions always apply. Existing snapshots and hosted history
are not erased when this file changes; the preview identifies historical paths
that are now excluded. Review or delete that history before connecting if needed.

Default capture budgets are 64 MiB per file, 512 MiB per scan/batch, and 64 MiB of
free disk reserved beyond the source size. Positive byte-count environment
variables `TRACE_MAX_FILE_BYTES`, `TRACE_MAX_CAPTURE_BYTES` and
`TRACE_MIN_FREE_BYTES` override them. Propagate these variables to the daemon
process and restart it after changing them. Limit failures preserve the previous
keyframe and surface as capture errors; hooks remain nonblocking.

Portable handoff bundles contain a consistent SQLite snapshot and referenced
objects, with mode 0600. Runtime credentials, logs, hook configuration and manual
companion databases are excluded. Bundles contain exact source bytes and must
still be treated as private. Import requires an idle/stopped daemon, rejects
archive links, path traversal, duplicate entries, excessive expansion and
truncated compression, and validates object hashes before committing history.
Database imports reject incompatible schemas and conflicting entity IDs rather
than silently losing rows. Importing the same unchanged history reports duplicates.
